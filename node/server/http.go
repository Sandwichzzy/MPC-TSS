package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	cmm "github.com/Sandwichzzy/tss/common"
	sign "github.com/Sandwichzzy/tss/node/signer"
	"github.com/Sandwichzzy/tss/node/tsslib"
)

type Server struct {
	logger    zerolog.Logger
	tssServer tsslib.Server
	signer    *sign.Processor
	s         *http.Server
	nonProd   bool
}

type GenRequest struct {
	PartyPubKeys []string `json:"party_pub_keys"`
	Threshold    int      `json:"threshold" mapstructure:"threshold"`
}

type MockEventRequest struct {
}

func NewHttpServer(addr string, t tsslib.Server, signer *sign.Processor, nonProd bool, jwtSecretStr string) (*Server, error) {
	hs := &Server{
		logger:    log.With().Str("module", "http").Logger(),
		tssServer: t,
		nonProd:   nonProd,
		signer:    signer,
	}
	handler, err := hs.newHandler(jwtSecretStr)
	if err != nil {
		return nil, err
	}
	s := &http.Server{
		Addr:    addr,
		Handler: handler,
	}
	hs.s = s
	return hs, nil
}

func (hs *Server) Start() error {
	if hs.s == nil {
		return errors.New("invalid http server instance")
	}
	if err := hs.s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		{
			hs.logger.Error().Err(err).Msg("api server starts failed")
			return err
		}
	}
	return nil
}

func (hs *Server) Stop() {
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := hs.s.Shutdown(c)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		hs.logger.Error().Err(err).Msg("Failed to shutdown the server gracefully")
	}
}

func (hs *Server) newHandler(jwtSecretStr string) (http.Handler, error) {
	router := gin.New()
	router.Use(logMiddleware()) // 日志
	router.Use(gin.Recovery())  // panic recover
	//JWT
	if jwtSecretStr != "" {
		return cmm.NewJwtHandler(router, jwtSecretStr)
	}

	// metrics
	router.GET(
		"/metrics",
		gin.WrapH(
			promhttp.InstrumentMetricHandler(
				prometheus.DefaultRegisterer,
				promhttp.HandlerFor(
					prometheus.DefaultGatherer,
					promhttp.HandlerOpts{
						MaxRequestsInFlight: 3,
					},
				),
			),
		),
	)

	return router, nil
}

func logMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		log.Debug().
			Str("route", c.Request.URL.Path).
			Str("port", c.Request.URL.Port()).
			Str("method", c.Request.Method).
			Msg("HTTP request received")

		c.Next()
	}
}
