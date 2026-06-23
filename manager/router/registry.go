package router

import (
	"errors"
	"net/http"

	tss "github.com/Sandwichzzy/tss/common"
	"github.com/Sandwichzzy/tss/manager/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// 对外提供 HTTP API
type Registry struct {
	signService types.ISignService
}

func NewRegistry(signService types.ISignService) *Registry {
	return &Registry{
		signService: signService,
	}
}

func (registry *Registry) KeygenHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var request tss.KeygenRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, errors.New("invalid request body"))
			return
		}
		cpk, err := registry.signService.Keygen(request)
		if err != nil {
			c.String(http.StatusInternalServerError, "failed to generate cluster public key")
			log.Error("failed to generate cluster public key", "error", err)
			return
		}
		if _, err = c.Writer.Write([]byte(cpk)); err != nil {
			log.Error("failed to write keygen response to response writer", "error", err)
		}
	}
}

func (registry *Registry) SignTxHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req tss.TransactionSignRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, errors.New("invalid request body"))
			return
		}
		if req.MessageHash == "" {
			c.JSON(http.StatusBadRequest, errors.New("message_hash must not be empty"))
			return
		}
		signature, err := registry.signService.TransactionSign(req)
		if err != nil {
			c.String(http.StatusInternalServerError, "failed to sign transaction")
			log.Error("failed to sign transaction", "error", err)
			return
		}
		if _, err := c.Writer.Write([]byte(signature)); err != nil {
			log.Error("failed to write signature to response writer", "error", err)
		}

	}
}

func (registry *Registry) PrometheusHandler() gin.HandlerFunc {
	h := promhttp.InstrumentMetricHandler(
		prometheus.DefaultRegisterer, promhttp.HandlerFor(
			prometheus.DefaultGatherer,
			promhttp.HandlerOpts{MaxRequestsInFlight: 3},
		),
	)
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}
