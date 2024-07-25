package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	envoyCore "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoyAuth "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	envoyType "github.com/envoyproxy/go-control-plane/envoy/type/v3"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"

	apipb "github.com/mrdan4es/tempo-example/pkg/api/v1"

	"github.com/mrdan4es/tempo-example/pkg/store"
)

const dsn = "postgres://test:test@postgres:5432/test?sslmode=disable"

func newExporter(ctx context.Context) (trace.SpanExporter, error) {
	exporter, err := otlptracegrpc.New(
		ctx,
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithEndpoint("tempo:4317"),
	)
	if err != nil {
		return nil, err
	}

	return exporter, nil
}

func newTraceProvider(ctx context.Context) (*trace.TracerProvider, error) {
	traceExporter, err := newExporter(ctx)
	if err != nil {
		return nil, err
	}

	traceRes, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceNameKey.String("APP_GRPC")),
	)
	if err != nil {
		return nil, err
	}

	traceProvider := trace.NewTracerProvider(
		trace.WithBatcher(traceExporter,
			trace.WithBatchTimeout(time.Second),
		),
		trace.WithResource(traceRes),
	)

	return traceProvider, nil
}

func setupOTelSDK(ctx context.Context) (shutdown func(context.Context) error, err error) {
	var shutdownFuncs []func(context.Context) error

	shutdown = func(ctx context.Context) error {
		var err error
		for _, fn := range shutdownFuncs {
			err = errors.Join(err, fn(ctx))
		}
		shutdownFuncs = nil
		return err
	}

	handleErr := func(inErr error) {
		err = errors.Join(inErr, shutdown(ctx))
	}

	prop := newPropagator()
	otel.SetTextMapPropagator(prop)

	tracerProvider, err := newTraceProvider(ctx)
	if err != nil {
		handleErr(err)
		return
	}

	shutdownFuncs = append(shutdownFuncs, tracerProvider.Shutdown)
	otel.SetTracerProvider(tracerProvider)

	// meterProvider, err := newMeterProvider()
	// if err != nil {
	// 	handleErr(err)
	// 	return
	// }
	// shutdownFuncs = append(shutdownFuncs, meterProvider.Shutdown)
	// otel.SetMeterProvider(meterProvider)
	// loggerProvider, err := newLoggerProvider()
	// if err != nil {
	// 	handleErr(err)
	// 	return
	// }
	// shutdownFuncs = append(shutdownFuncs, loggerProvider.Shutdown)
	// global.SetLoggerProvider(loggerProvider)

	return
}

func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

type UserStore interface {
	GetUser(context.Context, string) (*apipb.User, error)
	CheckUserPassword(ctx context.Context, username string, password string) error
}

type Server struct {
	apipb.UnimplementedTestServiceServer

	s UserStore
}

func NewServer(s UserStore) *Server {
	return &Server{
		s: s,
	}
}

func (s *Server) SayHello(_ context.Context, r *apipb.SayHelloRequest) (*apipb.SayHelloResponse, error) {
	fmt.Println("SayHello called")
	return &apipb.SayHelloResponse{Text: fmt.Sprintf("Hello %s!", r.Name)}, nil
}

func (s *Server) Check(ctx context.Context, r *envoyAuth.CheckRequest) (*envoyAuth.CheckResponse, error) {
	fmt.Println("Check called")

	raw, ok := strings.CutPrefix(r.Attributes.Request.Http.Headers["authorization"], "Basic ")
	if !ok {
		return responseDenied("Basic authentication required")
	}

	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return responseDenied("Basic authentication required")
	}

	auth := strings.Split(string(data), ":")
	if len(auth) != 2 {
		return responseDenied("Basic authentication required")
	}

	if err := s.s.CheckUserPassword(ctx, auth[0], auth[1]); err != nil {
		return responseDenied(err.Error())
	}

	user, err := s.s.GetUser(ctx, auth[0])
	if err != nil {
		return responseDenied(err.Error())
	}

	fmt.Println(user)

	return responseOk(user.Username)
}

func responseDenied(reason string) (*envoyAuth.CheckResponse, error) {
	return &envoyAuth.CheckResponse{
		Status: status.New(codes.Unauthenticated, reason).Proto(),
		HttpResponse: &envoyAuth.CheckResponse_DeniedResponse{
			DeniedResponse: &envoyAuth.DeniedHttpResponse{
				Status: &envoyType.HttpStatus{
					Code: envoyType.StatusCode_Unauthorized,
				},
				Headers: []*envoyCore.HeaderValueOption{},
			},
		},
	}, nil
}

func responseOk(value string) (*envoyAuth.CheckResponse, error) {
	return &envoyAuth.CheckResponse{
		Status: status.New(codes.OK, "").Proto(),
		HttpResponse: &envoyAuth.CheckResponse_OkResponse{
			OkResponse: &envoyAuth.OkHttpResponse{
				Headers: []*envoyCore.HeaderValueOption{
					{
						Header: &envoyCore.HeaderValue{
							Key:   "x-user-info",
							Value: value,
						},
						AppendAction: envoyCore.HeaderValueOption_APPEND_IF_EXISTS_OR_ADD,
					},
				},
			},
		},
	}, nil
}

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}

func run() (err error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	otelShutdown, err := setupOTelSDK(ctx)
	if err != nil {
		return
	}

	defer func() {
		_ = otelShutdown(context.Background())
	}()

	pgxCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse dsn: %w", err)
	}

	pgxCfg.ConnConfig.Tracer = otelpgx.NewTracer()

	pool, err := pgxpool.NewWithConfig(ctx, pgxCfg)
	if err != nil {
		return fmt.Errorf("pgxpool.New: %w", err)
	}
	defer pool.Close()

	if err = store.UpgradeDB(ctx, dsn); err != nil {
		return fmt.Errorf("upgrade db: %w", err)
	}

	lis, err := net.Listen("tcp", ":4321")
	if err != nil {
		return fmt.Errorf("create listener: %w", err)
	}

	serverRegister := grpc.NewServer(
		grpc.ChainUnaryInterceptor(),
		grpc.ChainStreamInterceptor(),
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	)

	go func() {
		<-ctx.Done()

		serverRegister.GracefulStop()
	}()

	s := NewServer(store.New(ctx, pool))

	apipb.RegisterTestServiceServer(serverRegister, s)
	envoyAuth.RegisterAuthorizationServer(serverRegister, s)

	fmt.Println("gRPC server started on port 4321")
	if err = serverRegister.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return err
	}

	return nil
}
