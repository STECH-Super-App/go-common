package middleware

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestLocaleFromContext_HeaderPresent(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(LocaleHeader, "kk")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	got := LocaleFromContext(c)

	if got != "kk" {
		t.Fatalf("LocaleFromContext = %q, want %q", got, "kk")
	}
}

func TestLocaleFromContext_HeaderAbsent(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	got := LocaleFromContext(c)

	if got != "" {
		t.Fatalf("LocaleFromContext = %q, want empty string", got)
	}
}

func TestLocaleMiddleware_StoresInContext(t *testing.T) {
	e := echo.New()
	e.Use(LocaleMiddleware())

	var captured string
	e.GET("/probe", func(c echo.Context) error {
		captured = localeFromCtx(c.Request().Context())
		return c.NoContent(204)
	})

	req := httptest.NewRequest("GET", "/probe", nil)
	req.Header.Set(LocaleHeader, "kk")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if captured != "kk" {
		t.Fatalf("ctx locale = %q, want %q", captured, "kk")
	}
}

func TestLocaleMiddleware_AbsentHeader(t *testing.T) {
	e := echo.New()
	e.Use(LocaleMiddleware())

	var captured string
	e.GET("/probe", func(c echo.Context) error {
		captured = localeFromCtx(c.Request().Context())
		return c.NoContent(204)
	})

	req := httptest.NewRequest("GET", "/probe", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if captured != "" {
		t.Fatalf("ctx locale = %q, want empty", captured)
	}
}

func TestLocaleFromGRPCContext_Present(t *testing.T) {
	md := metadata.New(map[string]string{LocaleMetadataKey: "ru"})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	if got := LocaleFromGRPCContext(ctx); got != "ru" {
		t.Fatalf("got %q, want %q", got, "ru")
	}
}

func TestLocaleFromGRPCContext_Absent(t *testing.T) {
	if got := LocaleFromGRPCContext(context.Background()); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestLocaleFromGRPCContext_MultiValueFirstWins(t *testing.T) {
	md := metadata.MD{LocaleMetadataKey: []string{"kk", "ru"}}
	ctx := metadata.NewIncomingContext(context.Background(), md)

	if got := LocaleFromGRPCContext(ctx); got != "kk" {
		t.Fatalf("got %q, want %q (first value)", got, "kk")
	}
}

func TestLocaleClientInterceptor_InjectsWhenCtxHasLocale(t *testing.T) {
	interceptor := LocaleClientInterceptor()

	var observedMD metadata.MD
	invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			t.Fatal("no outgoing metadata on ctx")
		}
		observedMD = md
		return nil
	}

	ctx := WithLocale(context.Background(), "kk")
	if err := interceptor(ctx, "/svc/Method", nil, nil, nil, invoker); err != nil {
		t.Fatalf("interceptor returned error: %v", err)
	}

	v := observedMD.Get(LocaleMetadataKey)
	if len(v) != 1 || v[0] != "kk" {
		t.Fatalf("metadata %v, want [kk]", v)
	}
}

func TestLocaleClientInterceptor_NoOpWhenCtxEmpty(t *testing.T) {
	interceptor := LocaleClientInterceptor()

	var observedMD metadata.MD
	var hadOutgoing bool
	invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		observedMD, hadOutgoing = metadata.FromOutgoingContext(ctx)
		return nil
	}

	if err := interceptor(context.Background(), "/svc/Method", nil, nil, nil, invoker); err != nil {
		t.Fatalf("interceptor returned error: %v", err)
	}

	if hadOutgoing && len(observedMD.Get(LocaleMetadataKey)) != 0 {
		t.Fatalf("interceptor injected metadata when ctx had no locale: %v", observedMD)
	}
}
