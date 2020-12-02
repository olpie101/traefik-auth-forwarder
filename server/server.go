package server

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	proxymiddleware "github.com/olpie101/traefik-auth-forwarder/server/middleware"
	"go.uber.org/zap"
)

const (
	xForwardedProto  = "X-Forwarded-Proto"
	xForwardedHost   = "X-Forwarded-Host"
	xForwardedUri    = "X-Forwarded-Uri"
	xForwardedMethod = "X-Forwarded-Method"
)

type Server struct {
	decisionUrl string
	copyHeaders []string
	m           http.Handler
	c           *http.Client
	logger      *zap.SugaredLogger
	redirectURL string
}

type Config struct {
	Address        string
	ForwardAddress string
	Headers        []string
	RedirectURL    string
}

func New(c *http.Client, cfg Config, logger *zap.SugaredLogger) (http.Handler, error) {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	srv := &Server{
		decisionUrl: cfg.ForwardAddress,
		copyHeaders: cfg.Headers,
		m:           r,
		c:           c,
		logger:      logger,
		redirectURL: cfg.RedirectURL,
	}

	headerSet := make(map[string]struct{}, 4)
	for _, h := range cfg.Headers {
		headerSet[h] = struct{}{}
	}

	defaultHeaders := []string{
		xForwardedProto,
		xForwardedHost,
		xForwardedUri,
		xForwardedMethod,
	}

	for _, h := range defaultHeaders {
		headerSet[h] = struct{}{}
	}

	copyHeaders := make([]string, 0, len(headerSet))
	for h := range headerSet {
		copyHeaders = append(copyHeaders, h)
	}

	srv.copyHeaders = copyHeaders
	metricsMiddleware, metricsHandler, err := proxymiddleware.PrometheusHandler(xForwardedHost)
	if err != nil {
		return nil, err
	}

	r.Route("/decision", func(r chi.Router) {
		r.Use(metricsMiddleware)
		r.Handle("/*", srv.decisionHandler())
	})
	r.Get("/health", func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
	})
	r.Get("/metrics", metricsHandler)

	srv.logger.Infow("created routes", "forward-url", srv.decisionUrl, "headers", srv.copyHeaders)
	return r, nil
}

func (srv *Server) decisionHandler() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		srv.logger.Infow("incomming request", "request", r.Header)
		outreq, err := decisionRequest(r, srv.decisionUrl)
		if err != nil {
			srv.logger.Infow("error processing request", "error", err)
			rw.WriteHeader(500)
			return
		}
		srv.copyRequestHeaders(r, outreq)

		srv.logger.Infow("performing forward request", "url", outreq.URL.String())
		res, err := srv.c.Do(outreq)
		if err != nil {
			srv.logger.Infow("error forwarding request", "error", err)
			rw.WriteHeader(500)
			return
		}
		defer res.Body.Close()

		srv.logger.Infow("auth response", "code", res.StatusCode, "status", res.Status, "headers", res.Header, "uri", res.Request.URL.String())

		if res.StatusCode != http.StatusOK {
			srv.handleResponseErrorStatus(res, rw)
			return
		}

		copyResponseHeaders(res, rw)
		rw.WriteHeader(http.StatusOK)
		srv.logger.Infow("decision response", "status", res.StatusCode)

	}
}

func (srv *Server) handleResponseErrorStatus(res *http.Response, rw http.ResponseWriter) {
	code := res.StatusCode
	d, err := ioutil.ReadAll(res.Body)
	if err != nil {
		srv.logger.Infow("error decoding decision respons")
		rw.WriteHeader(500)
		return
	}

	if code == http.StatusUnauthorized {
		rw.Header().Set("Location", srv.redirectURL)
		rw.WriteHeader(http.StatusTemporaryRedirect)
		return
	}

	srv.logger.Infow("error forwarding request")
	rw.WriteHeader(res.StatusCode)
	rw.Write(d)
}

func decisionRequest(inreq *http.Request, decisionUrl string) (*http.Request, error) {
	host := inreq.Header.Get(xForwardedHost)
	path := inreq.Header.Get(xForwardedUri)

	url := fmt.Sprintf("%s/%s%s", decisionUrl, host, path)
	outreq, err := http.NewRequest(inreq.Method, url, nil)
	if err != nil {
		return nil, err
	}

	return outreq, nil
}

func (srv *Server) copyRequestHeaders(inreq, outreq *http.Request) {
	for _, h := range srv.copyHeaders {
		if v := inreq.Header.Get(h); len(v) > 0 {
			outreq.Header.Set(h, v)
		}
	}
}

func copyResponseHeaders(inres *http.Response, outres http.ResponseWriter) {
	for h, v := range inres.Header {
		if existingHeader := outres.Header().Get(h); len(existingHeader) == 0 && len(v) > 0 {
			outres.Header().Set(h, strings.Join(v, ","))
		}
	}
}
