// Code generated by 'freedom new-project event-example'
package main

import (
	"fmt"
	"time"

	"github.com/8treenet/freedom"
	_ "github.com/8treenet/freedom/example/event-example/adapter/controllers"
	"github.com/8treenet/freedom/example/event-example/server/conf"
	"github.com/8treenet/freedom/infra/kafka"
	"github.com/8treenet/freedom/infra/requests"
	"github.com/8treenet/freedom/middleware"
	"github.com/Shopify/sarama"
	"github.com/prometheus/client_golang/prometheus"
)

// mac: Start kafka: zookeeper-server-start /usr/local/etc/kafka/zookeeper.properties & kafka-server-start /usr/local/etc/kafka/server.properties
func main() {
	// If you use the default Kafka configuration, no need to set
	kafka.SettingConfig(func(conf *sarama.Config, other map[string]interface{}) {
		conf.Producer.Retry.Max = 3
		conf.Producer.Retry.Backoff = 5 * time.Second
		conf.Consumer.Offsets.Initial = sarama.OffsetOldest
		fmt.Println(other)
	})
	app := freedom.NewApplication()
	installMiddleware(app)
	addrRunner := app.CreateH2CRunner(conf.Get().App.Other["listen_addr"].(string))

	// Obtain and install the kafka infrastructure for domain events
	app.InstallDomainEventInfra(kafka.GetDomainEventInfra())
	//app.InstallParty("/event-example")
	liveness(app)
	app.Run(addrRunner, *conf.Get().App)
}

func installMiddleware(app freedom.Application) {
	//Recover中间件
	app.InstallMiddleware(middleware.NewRecover())
	//Trace链路中间件
	app.InstallMiddleware(middleware.NewTrace("x-request-id"))
	//日志中间件，每个请求一个logger
	app.InstallMiddleware(middleware.NewRequestLogger("x-request-id"))
	//logRow中间件，每一行日志都会触发回调。如果返回true，将停止中间件遍历回调。
	app.Logger().Handle(middleware.DefaultLogRowHandle)
	//HttpClient 普罗米修斯中间件，监控ClientAPI的请求。
	middle := middleware.NewClientPrometheus(conf.Get().App.Other["service_name"].(string), freedom.Prometheus())
	requests.InstallMiddleware(middle)
	//总线中间件，处理上下游透传的Header
	app.InstallBusMiddleware(middleware.NewBusFilter())

	//安装事件监控中间件
	eventMiddle := NewEventPrometheus(conf.Get().App.Other["service_name"].(string))
	kafka.InstallMiddleware(eventMiddle)
	//安装自定义消息中间件
	kafka.InstallMiddleware(newProducerMiddleware())
}

func liveness(app freedom.Application) {
	app.Iris().Get("/ping", func(ctx freedom.Context) {
		ctx.WriteString("pong")
	})
}

// 自定义一个日志记录消息中间件
func newProducerMiddleware() kafka.ProducerHandler {
	return func(msg *kafka.Msg) {
		//日志记录生产消息
		now := time.Now()
		msg.Next()
		diff := time.Now().Sub(now)

		if err := msg.GetExecution(); err != nil {
			freedom.Logger().Error(string(msg.Content), freedom.LogFields{
				"topic":    msg.Topic,
				"duration": diff.Milliseconds(),
				"error":    err.Error(),
				"title":    "producer",
			})
			return
		}
		freedom.Logger().Info(string(msg.Content), freedom.LogFields{
			"topic":    msg.Topic,
			"duration": diff.Milliseconds(),
			"title":    "producer",
		})
	}
}

// NewEventPrometheus 事件监控中间件
func NewEventPrometheus(serviceName string) kafka.ProducerHandler {
	eventPublishReqs := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name:        "event_publish_total",
			Help:        "",
			ConstLabels: prometheus.Labels{"service": serviceName},
		},
		[]string{"event", "error"},
	)
	freedom.Prometheus().RegisterCounter(eventPublishReqs)

	return func(msg *kafka.Msg) {
		if msg.IsStopped() {
			return
		}
		msg.Next()

		if msg.GetExecution() != nil {
			eventPublishReqs.WithLabelValues(msg.Topic, msg.GetExecution().Error()).Inc()
			return
		}
		eventPublishReqs.WithLabelValues(msg.Topic, "").Inc()
	}
}
