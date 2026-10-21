package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"

	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

type telemetry struct {
	metricsHandler http.Handler
	shutdown       func(context.Context) error
}

func setupTelemetry(ctx context.Context) (*telemetry, error) {
	serviceName := strings.TrimSpace(os.Getenv("OTEL_SERVICE_NAME"))
	if serviceName == "" {
		serviceName = "family-dashboard"
	}
	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, err
	}

	registry := promclient.NewRegistry()
	metricExporter, err := otelprom.New(
		otelprom.WithRegisterer(registry),
		otelprom.WithResourceAsConstantLabels(attribute.NewAllowKeysFilter("service.name")),
	)
	if err != nil {
		return nil, err
	}
	metricProvider := metric.NewMeterProvider(metric.WithReader(metricExporter), metric.WithResource(res))
	otel.SetMeterProvider(metricProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	traceOptions := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	var traceExporter sdktrace.SpanExporter
	if strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")) != "" || strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "" {
		traceExporter, err = otlptracehttp.New(ctx)
		if err != nil {
			_ = metricProvider.Shutdown(ctx)
			return nil, err
		}
		traceOptions = append(traceOptions, sdktrace.WithBatcher(traceExporter))
	}
	traceProvider := sdktrace.NewTracerProvider(traceOptions...)
	otel.SetTracerProvider(traceProvider)

	return &telemetry{
		metricsHandler: promhttp.HandlerFor(registry, promhttp.HandlerOpts{}),
		shutdown: func(ctx context.Context) error {
			traceErr := traceProvider.Shutdown(ctx)
			metricErr := metricProvider.Shutdown(ctx)
			return errors.Join(traceErr, metricErr)
		},
	}, nil
}

func instrumentHTTP(mux *http.ServeMux) http.Handler {
	routeAware := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler, route := mux.Handler(r)
		if route != "" {
			handler = otelhttp.WithRouteTag(route, handler)
		}
		handler.ServeHTTP(w, r)
	})
	return otelhttp.NewHandler(routeAware, "http.server",
		otelhttp.WithFilter(func(r *http.Request) bool {
			return r.URL.Path != "/healthz" && r.URL.Path != "/metrics"
		}),
	)
}

// tracedRequest records only the destination host and HTTP status. In particular,
// it never records a request URL, which can contain calendar credentials or API keys.
func tracedRequest(ctx context.Context, operation string, client *http.Client, req *http.Request) (*http.Response, error) {
	ctx, span := otel.Tracer("family-dashboard").Start(ctx, operation,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("server.address", req.URL.Host),
			attribute.String("http.request.method", req.Method),
		),
	)
	defer span.End()

	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		// Avoid recording the raw error: net/http errors may contain the full URL.
		span.SetAttributes(attribute.String("error.type", "network_error"))
		span.SetStatus(codes.Error, "outbound request failed")
		return nil, err
	}
	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
	if resp.StatusCode >= http.StatusInternalServerError {
		span.SetStatus(codes.Error, "upstream server error")
	}
	return resp, nil
}
