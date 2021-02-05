package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/konfortes/go-server-utils/server"
	"github.com/konfortes/go-server-utils/utils"
	"github.com/opentracing/opentracing-go"
)

type appConfig struct {
	handleTimeMs int
	errorRate    int
	forwardTo    []string
	callParallel bool
}

func (ac *appConfig) load() {
	ac.handleTimeMs, _ = strconv.Atoi(utils.GetEnvOr("HANDLE_TIME", "100"))
	ac.errorRate, _ = strconv.Atoi(utils.GetEnvOr("ERROR_RATE", "0"))
	ac.callParallel = utils.GetEnvOr("CALL_PARALLEL", "false") == "true"

	forwardSlice := strings.Split(utils.GetEnvOr("FORWARD_TO", ""), ",")
	for _, url := range forwardSlice {
		if len(url) > 0 {
			ac.forwardTo = append(ac.forwardTo, url)
		}
	}
}

var (
	config appConfig
)

func main() {
	config.load()

	serverConfig := server.Config{
		AppName:       utils.GetEnvOr("APP_NAME", "tracing-demo"),
		Port:          utils.GetEnvOr("PORT", "3000"),
		Env:           utils.GetEnvOr("GO_ENV", "development"),
		Handlers:      handlers(),
		ShutdownHooks: []func(){func() { log.Println("bye bye") }},
		WithTracing:   true,
	}

	srv := server.Initialize(serverConfig)

	go func() {
		log.Println("listening on " + srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	server.GracefulShutdown(srv)
}

func handlers() []server.Handler {
	return []server.Handler{
		{Method: http.MethodGet, Pattern: "/", H: func(c *gin.Context) {
			// do something inside the service
			if err := doSomething(); err != nil {
				c.Writer.WriteHeader(http.StatusInternalServerError)
				return
			}

			// call outside services
			if err := callServices(c.Request.Context()); err != nil {
				c.Writer.WriteHeader(http.StatusInternalServerError)
				return
			}

			c.Writer.WriteHeader(204)
		}},
	}
}

func doSomething() error {
	// synthetic handle time
	time.Sleep(time.Duration(config.handleTimeMs) * time.Millisecond)

	// synthetic error rate
	rand.Seed(time.Now().UnixNano())
	errorRandom := rand.Intn(100)

	if errorRandom < config.errorRate {
		return errors.New("error")
	}

	return nil
}

func callServices(ctx context.Context) error {
	if len(config.forwardTo) == 0 {
		return nil
	}

	if config.callParallel {
		return callParallel(ctx, config.forwardTo)
	} else {
		return call(ctx, config.forwardTo)
	}
}

func call(ctx context.Context, services []string) error {
	httpClient := &http.Client{}
	currentSpan := opentracing.SpanFromContext(ctx)

	for _, serviceURL := range services {
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/", serviceURL), nil)

		opentracing.GlobalTracer().Inject(
			currentSpan.Context(),
			opentracing.HTTPHeaders,
			opentracing.HTTPHeadersCarrier(req.Header),
		)

		res, _ := httpClient.Do(req)

		defer res.Body.Close()

		if res.StatusCode >= 500 {
			return errors.New("error")
		}
	}
	return nil
}

func callParallel(ctx context.Context, services []string) error {
	var wg sync.WaitGroup
	hasError := false
	for _, serviceURL := range services {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/", url), nil)
			httpClient := &http.Client{}

			currentSpan := opentracing.SpanFromContext(ctx)
			opentracing.GlobalTracer().Inject(
				currentSpan.Context(),
				opentracing.HTTPHeaders,
				opentracing.HTTPHeadersCarrier(req.Header),
			)

			res, _ := httpClient.Do(req)

			defer res.Body.Close()

			if res.StatusCode >= 500 {
				hasError = true
			}
		}(serviceURL)
	}

	wg.Wait()
	if hasError {
		return errors.New("error")
	}

	return nil
}
