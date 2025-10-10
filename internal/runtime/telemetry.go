package runtime

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/mohammad-safakhou/newser/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

// Telemetry encapsulates tracer and meter providers.
type Telemetry struct {
	tp *sdktrace.TracerProvider
	mp *sdkmetric.MeterProvider
}

// TelemetryOptions configures telemetry initialization.
type TelemetryOptions struct {
	ServiceName    string
	ServiceVersion string
	MetricsPort    int
}

// SetupTelemetry initializes tracing and metrics for a service.
func SetupTelemetry(ctx context.Context, cfg config.TelemetryConfig, opts TelemetryOptions) (*Telemetry, otelmetric.Meter, trace.Tracer, error) {
	if !cfg.Enabled {
		return &Telemetry{}, otel.Meter(opts.ServiceName), otel.Tracer(opts.ServiceName), nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(opts.ServiceName),
			attribute.String("service.namespace", "newser"),
			attribute.String("service.version", opts.ServiceVersion),
		),
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resource init: %w", err)
	}

	endpoint := cfg.OTLPEndpoint
	if endpoint == "" {
		endpoint = "localhost:4317"
	}
	traceExporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithDialOption(grpc.WithBlock()),
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("otlp init: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	tracer := tp.Tracer(opts.ServiceName)

	promRegistry := prometheus.NewRegistry()
	promExporter, err := promexporter.New(promexporter.WithRegisterer(promRegistry))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("prom exporter: %w", err)
	}
	metricExporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(endpoint),
		otlpmetricgrpc.WithInsecure(),
		otlpmetricgrpc.WithDialOption(grpc.WithBlock()),
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("otlp metric init: %w", err)
	}
	periodicReader := sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(15*time.Second))
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(promExporter),
		sdkmetric.WithReader(periodicReader),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)
	meter := mp.Meter(opts.ServiceName)

	if opts.MetricsPort > 0 {
		go func() {
			mux := http.NewServeMux()
			mux.Handle("/metrics", promhttp.HandlerFor(promRegistry, promhttp.HandlerOpts{}))
			server := &http.Server{
				Addr:              fmt.Sprintf(":%d", opts.MetricsPort),
				Handler:           mux,
				ReadHeaderTimeout: 5 * time.Second,
			}
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				fmt.Printf("metrics server error: %v\n", err)
			}
		}()
	}

	return &Telemetry{tp: tp, mp: mp}, meter, tracer, nil
}

// Shutdown flushes providers.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	if t == nil {
		return nil
	}
	var err error
	if t.tp != nil {
		if e := t.tp.Shutdown(ctx); e != nil {
			err = fmt.Errorf("trace shutdown: %w", e)
		}
	}
	if t.mp != nil {
		if e := t.mp.Shutdown(ctx); e != nil {
			if err != nil {
				err = fmt.Errorf("%v; metric shutdown: %w", err, e)
			} else {
				err = fmt.Errorf("metric shutdown: %w", e)
			}
		}
	}
	return err
}
