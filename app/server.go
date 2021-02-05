package main

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/konfortes/go-server-utils/logging"
	"github.com/konfortes/go-server-utils/server"
	"github.com/konfortes/go-server-utils/utils"
	"github.com/opentracing/opentracing-go"
)

type appConfig struct {
	handleTimeMs int
	errorRate    int
	forwardTo    []string
}

func (ac *appConfig) load() {
	ac.handleTimeMs, _ = strconv.Atoi(utils.GetEnvOr("HANDLE_TIME", "100"))
	ac.errorRate, _ = strconv.Atoi(utils.GetEnvOr("ERROR_RATE", "0"))

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
			logger := logging.Logger(c.Request.Context())
			logger.Info("Got request")
			logger.Infof("Handle time of %d ms", config.handleTimeMs)
			logger.Infof("error rate of %d", config.errorRate)

			rand.Seed(time.Now().UnixNano())
			errorRandom := rand.Intn(100)

			// handle time
			time.Sleep(time.Duration(config.handleTimeMs) * time.Millisecond)

			// error rate
			if errorRandom < config.errorRate {
				time.Sleep(time.Duration(config.handleTimeMs) * time.Millisecond)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal error"})
				return
			}

			if len(config.forwardTo) == 0 {
				logger.Info("nothing to forward")
				c.Writer.WriteHeader(204)
				return
			}

			var wg sync.WaitGroup
			hasError := false
			for _, url := range config.forwardTo {
				wg.Add(1)
				go func(url string) {
					defer wg.Done()
					req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/", url), nil)
					httpClient := &http.Client{}

					currentSpan := opentracing.SpanFromContext(c.Request.Context())
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
				}(url)
			}

			wg.Wait()
			if hasError {
				c.Writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			c.Writer.WriteHeader(204)
		}},
	}
}
