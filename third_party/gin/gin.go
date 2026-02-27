package gin

import (
	"net/http"
)

type HandlerFunc func(*Context)

type Context struct {
	Writer  http.ResponseWriter
	Request *http.Request
}

type Engine struct {
	mux *http.ServeMux
}

func New() *Engine {
	return &Engine{mux: http.NewServeMux()}
}

func Default() *Engine { return New() }

func WrapF(h http.HandlerFunc) HandlerFunc {
	return func(c *Context) { h(c.Writer, c.Request) }
}

func (e *Engine) add(method, path string, h HandlerFunc) {
	e.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if method != "" && r.Method != method && r.Method != http.MethodOptions {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write([]byte(`{"detail":"method not allowed"}`))
			return
		}
		h(&Context{Writer: w, Request: r})
	})
}

func (e *Engine) GET(path string, h HandlerFunc)  { e.add(http.MethodGet, path, h) }
func (e *Engine) POST(path string, h HandlerFunc) { e.add(http.MethodPost, path, h) }
func (e *Engine) Any(path string, h HandlerFunc)  { e.add("", path, h) }

func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) { e.mux.ServeHTTP(w, r) }

func (e *Engine) Run(addr ...string) error {
	a := ":8080"
	if len(addr) > 0 && addr[0] != "" {
		a = addr[0]
	}
	return http.ListenAndServe(a, e)
}
