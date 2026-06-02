package metrics

import (
	"github.com/go-kit/kit/metrics"
	"github.com/go-kit/kit/metrics/prometheus"
	stdprometheus "github.com/prometheus/client_golang/prometheus"
)

// Metrics contains metrics exposed by this package.
// 封装 Prometheus 指标（Metrics）定义和初始化逻辑，供业务代码直接使用，用来统计：
//
// 在线节点数量（OnlineNodesCount）
// 签名失败次数（SignCount）
// 审批数量（ApproveNumber）
type Metrics struct {
	OnlineNodesCount metrics.Gauge
	SignCount        metrics.Gauge
	ApproveNumber    metrics.Gauge
}

// 工厂函数 用于创建并返回 Metrics。
func PrometheusMetrics(namespace string, labelsAndValues ...string) *Metrics {

	var labels []string
	for i := 0; i < len(labelsAndValues); i += 2 {
		labels = append(labels, labelsAndValues[i])
	}

	var sign = prometheus.NewGaugeFrom(
		stdprometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "sign",
			Name:      "sign_failed_counter",
			Help:      "sign failed ",
		}, labels).With(labelsAndValues...)
	var approve = prometheus.NewGaugeFrom(
		stdprometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "sign",
			Name:      "approve_counter",
			Help:      "approve number ",
		}, labels).With(labelsAndValues...)
	var online = prometheus.NewGaugeFrom(
		stdprometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "node",
			Name:      "online_node_counter",
			Help:      "online node number ",
		}, labels).With(labelsAndValues...)
	return &Metrics{
		OnlineNodesCount: online,
		SignCount:        sign,
		ApproveNumber:    approve,
	}
}
