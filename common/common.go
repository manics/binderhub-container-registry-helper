package common

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const AUTH_TOKEN_ENV_VAR = "BINDERHUB_AUTH_TOKEN" // #nosec G101 -- Name of an env-var, not a secret

// healthHandler is a http.handler that returns the version
type healthHandler struct {
	healthInfo *map[string]string
}

// ServeHTTP implements http.Handler
func (h *healthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("content-type", "application/json")
	if r.Method == http.MethodGet && r.URL.Path == "/health" {
		jsonBytes, err := json.Marshal(*h.healthInfo)
		if err != nil {
			log.Println("ERROR:", err)
			InternalServerError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, errw := w.Write(jsonBytes)
		if errw != nil {
			log.Println("ERROR:", errw)
		}
	} else {
		NotFound(w, r)
		return
	}
}

func getAuthToken() (string, error) {
	authToken, found := os.LookupEnv(AUTH_TOKEN_ENV_VAR)
	if !found {
		return "", fmt.Errorf("%s not found, set it to a secret token or '' to disable authentication", AUTH_TOKEN_ENV_VAR)
	}
	return authToken, nil
}

// The main entrypoint for the service
func Run(registryH IRegistryClient, healthInfo map[string]string, listen string, promRegistry *prometheus.Registry) {
	promHandler := promhttp.HandlerFor(
		promRegistry,
		promhttp.HandlerOpts{EnableOpenMetrics: true},
	)

	authToken, err := getAuthToken()
	if err != nil {
		log.Fatalln(err)
	}

	health := healthHandler{
		healthInfo: &healthInfo,
	}

	mux := http.NewServeMux()
	mux.Handle("/health", &health)
	mux.Handle("/metrics", promHandler)

	CreateServer(mux, registryH, authToken, promRegistry)

	log.Printf("Listening on %v\n", listen)
	server := &http.Server{
		Addr:         listen,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	errw := server.ListenAndServe()
	if errw != nil {
		log.Fatalln(errw)
	}
}
