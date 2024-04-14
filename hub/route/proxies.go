package route

import (
	"context"
	"errors"
	"github.com/Tension-Z/clash/log"
	"net/http"
	"strconv"
	"time"

	"github.com/Tension-Z/clash/adapter"
	"github.com/Tension-Z/clash/adapter/outboundgroup"
	"github.com/Tension-Z/clash/component/profile/cachefile"
	C "github.com/Tension-Z/clash/constant"
	"github.com/Tension-Z/clash/tunnel"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
)

func proxyRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", getProxies)

	r.Post("/delay", queryProxyDelay)
	r.Put("/switch", SwitchProxy)

	r.Route("/{name}", func(r chi.Router) {
		r.Use(parseProxyName, findProxyByName)
		r.Get("/", getProxy)
		r.Get("/delay", getProxyDelay)
		r.Put("/", SwitchProxy)
	})
	return r
}

type ProxyDelayRequest struct {
	Name    string `json:"name"`
	Timeout int64  `json:"timeout"`
	Url     string `json:"url"`
}

func queryProxyDelay(w http.ResponseWriter, r *http.Request) {
	data := &ProxyDelayRequest{}
	if err := render.DecodeJSON(r.Body, data); err != nil {
		render.Status(r, http.StatusOK)
		render.JSON(w, r, render.M{
			"code": 400,
			"msg":  "参数错误",
		})
		return
	}
	if data.Timeout == 0 {
		data.Timeout = 1000
	}
	if len(data.Url) == 0 {
		data.Url = "http://www.gstatic.com/generate_204"
	}
	// 获取代理
	proxy, err := queryProxyByName(data.Name)
	if err != nil {
		render.Status(r, http.StatusOK)
		render.JSON(w, r, render.M{
			"code": 404,
			"msg":  "代理不存在",
		})
		return
	}
	// 获取延迟
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*time.Duration(data.Timeout))
	defer cancel()
	delay, meanDelay, err := proxy.(C.Proxy).URLTest(ctx, data.Url)
	if ctx.Err() != nil {
		render.Status(r, http.StatusOK)
		render.JSON(w, r, render.M{
			"code": 504,
			"msg":  "请求超时",
		})
		return
	}
	if err != nil || delay == 0 {
		render.Status(r, http.StatusOK)
		render.JSON(w, r, render.M{
			"code": 500,
			"msg":  "延迟测试出错",
		})
		return
	}

	render.JSON(w, r, render.M{
		"code": 200,
		"msg":  "success",
		"data": render.M{"delay": delay, "meanDelay": meanDelay},
	})
}

func parseProxyName(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := getEscapeParam(r, "name")
		ctx := context.WithValue(r.Context(), CtxKeyProxyName, name)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func findProxyByName(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.Context().Value(CtxKeyProxyName).(string)
		proxies := tunnel.Proxies()
		proxy, exist := proxies[name]
		if !exist {
			render.Status(r, http.StatusNotFound)
			render.JSON(w, r, ErrNotFound)
			return
		}

		ctx := context.WithValue(r.Context(), CtxKeyProxy, proxy)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func getProxies(w http.ResponseWriter, r *http.Request) {
	proxies := tunnel.Proxies()
	render.JSON(w, r, render.M{
		"proxies": proxies,
	})
}

func getProxy(w http.ResponseWriter, r *http.Request) {
	proxy := r.Context().Value(CtxKeyProxy).(C.Proxy)
	render.JSON(w, r, proxy)
}

func updateProxy(w http.ResponseWriter, r *http.Request) {
	req := struct {
		Name string `json:"name"`
	}{}
	if err := render.DecodeJSON(r.Body, &req); err != nil {
		render.Status(r, http.StatusOK)
		render.JSON(w, r, render.M{
			"code": 400,
			"msg":  "参数错误",
		})
		return
	}

	proxy := r.Context().Value(CtxKeyProxy).(*adapter.Proxy)
	selector, ok := proxy.ProxyAdapter.(*outboundgroup.Selector)
	if !ok {
		render.Status(r, http.StatusOK)
		render.JSON(w, r, render.M{
			"code": 500,
			"msg":  "代理类型错误",
		})
		return
	}

	if err := selector.Set(req.Name); err != nil {
		render.Status(r, http.StatusOK)
		render.JSON(w, r, render.M{
			"code": 500,
			"msg":  "切换节点失败",
		})
		return
	}

	cachefile.Cache().SetSelected(proxy.Name(), req.Name)
	//log.Infoln("切换节点到 %v", req.Name)
	//render.NoContent(w, r)
	render.JSON(w, r, render.M{
		"code": 200,
		"msg":  "success",
	})
}

func getProxyDelay(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	url := query.Get("url")
	timeout, err := strconv.ParseInt(query.Get("timeout"), 10, 16)
	if err != nil {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, ErrBadRequest)
		return
	}

	proxy := r.Context().Value(CtxKeyProxy).(C.Proxy)

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*time.Duration(timeout))
	defer cancel()

	delay, meanDelay, err := proxy.URLTest(ctx, url)
	if ctx.Err() != nil {
		render.Status(r, http.StatusGatewayTimeout)
		render.JSON(w, r, ErrRequestTimeout)
		return
	}

	if err != nil || delay == 0 {
		render.Status(r, http.StatusServiceUnavailable)
		render.JSON(w, r, newError("An error occurred in the delay test"))
		return
	}

	render.JSON(w, r, render.M{
		"delay":     delay,
		"meanDelay": meanDelay,
	})
}

func SwitchProxy(w http.ResponseWriter, r *http.Request) {
	req := struct {
		Name  string `json:"name"`
		Class string `json:"class"`
	}{}
	if err := render.DecodeJSON(r.Body, &req); err != nil {
		render.Status(r, http.StatusOK)
		render.JSON(w, r, render.M{
			"code": 400,
			"msg":  "参数错误",
		})
		return
	}
	// 获取代理组
	proxys, err := queryProxyByName(req.Class)
	if err != nil {
		render.Status(r, http.StatusOK)
		render.JSON(w, r, render.M{
			"code": 404,
			"msg":  "代理不存在",
		})
		return
	}
	selector, ok := proxys.(*adapter.Proxy).ProxyAdapter.(*outboundgroup.Selector)
	if !ok {
		render.Status(r, http.StatusOK)
		render.JSON(w, r, render.M{
			"code": 500,
			"msg":  "代理类型错误",
		})
		return
	}
	// 切换代理
	if err := selector.Set(req.Name); err != nil {
		render.Status(r, http.StatusOK)
		render.JSON(w, r, render.M{
			"code": 500,
			"msg":  "切换节点失败",
		})
		return
	}
	cachefile.Cache().SetSelected(proxys.(*adapter.Proxy).Name(), req.Name)
	log.Infoln("切换节点到 %v", req.Name)
	//render.NoContent(w, r)
	render.JSON(w, r, render.M{
		"code": 200,
		"msg":  "success",
	})
}

func queryProxyByName(name string) (any, error) {
	proxies := tunnel.Proxies()
	proxy, exist := proxies[name]
	if !exist {
		return nil, errors.New("proxy not found")
	}
	return proxy, nil
}
