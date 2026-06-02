package router

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (registry *Registry) Register(r *gin.Engine) {
	r.GET("/ping", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	v1Router := r.Group("/api/v1")
	v1Router.POST("/keygen", registry.KeygenHandler())
	v1Router.POST("/sign", registry.SignTxHandler())
	v1Router.GET("/metrics", registry.PrometheusHandler())
}
