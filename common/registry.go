// Based on https://golang.cafe/blog/golang-rest-api-example.html

// package common contains common types and functions used by implementations
package common

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// RegistryToken is an object containing a username and password that can be used
// to login to a registry and an Expires time when the token will expire
type RegistryToken struct {
	Username string    `json:"username"`
	Password string    `json:"password"`
	Registry string    `json:"registry"`
	Expires  time.Time `json:"expires"`
}

var httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: "binderhub_container_registry_helper",
	Name:      "api_response_time_seconds",
	Help:      "Duration of API requests.",
}, []string{"method", "path", "status"})

// CheckAuthorised wraps originalHandler to check for a valid Authorization header
// and returns a http.Handler
func CheckAuthorised(originalHandler http.Handler, authToken string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorised := false
		if authToken == "" {
			authorised = true
		} else {
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				bearerToken := strings.TrimPrefix(authHeader, "Bearer ")
				authorised = (authToken == bearerToken)
			}
		}

		if !authorised {
			NotAuthorised(w, r)
			return
		}
		originalHandler.ServeHTTP(w, r)
	})
}

// InternalServerError is a handler that returns a 500 HTTP error
func InternalServerError(w http.ResponseWriter, r *http.Request, errorResponse error) {
	errObj := map[string]string{
		"error": errorResponse.Error(),
	}
	jsonBytes := []byte(`{"error": "internal server error"}`)
	var err error

	return_error := strings.ToLower(os.Getenv("RETURN_ERROR_DETAILS"))
	for _, v := range []string{"true", "1", "yes"} {
		if return_error == v {
			jsonBytes, err = json.Marshal(errObj)
			if err != nil {
				log.Println("ERROR:", err)
			}
			break
		}
	}
	jsonBytes = append(jsonBytes, byte('\n'))
	w.WriteHeader(http.StatusInternalServerError)
	_, errw := w.Write(jsonBytes)
	if errw != nil {
		log.Println("ERROR:", errw)
	}
}

// NotFound is a handler that returns a 404 HTTP error
func NotFound(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	_, errw := w.Write([]byte("null\n"))
	if errw != nil {
		log.Println("ERROR:", errw)
	}
}

// NotAuthorised is a handler that returns a 403 HTTP error
func NotAuthorised(w http.ResponseWriter, r *http.Request) {
	fmt.Println("NotAuthorised %r", r)
	w.WriteHeader(http.StatusForbidden)
	_, errw := w.Write([]byte(`{"error": "not authorised"}` + "\n"))
	if errw != nil {
		log.Println("ERROR:", errw)
	}
}

// RepoGetName extracts the repository name from the request path
func RepoGetName(r *http.Request) (string, error) {
	if !strings.HasPrefix(r.URL.Path, "/repo/") {
		err := fmt.Sprintf("Invalid path: %s", r.URL.Path)
		return "", errors.New(err)
	}
	name := strings.TrimPrefix(r.URL.Path, "/repo/")
	return name, nil
}

// ImageGetNameAndTag extracts the repository name and tag from the request path
func ImageGetNameAndTag(r *http.Request) (string, string, error) {
	if !strings.HasPrefix(r.URL.Path, "/image/") {
		err := fmt.Sprintf("Invalid path: %s", r.URL.Path)
		return "", "", errors.New(err)
	}

	fullname := strings.TrimPrefix(r.URL.Path, "/image/")
	repoName := fullname
	tag := "latest"
	sep := strings.LastIndex(fullname, ":")
	if sep > -1 {
		repoName = fullname[:sep]
		tag = fullname[sep+1:]
	}

	if tag == "" {
		err := fmt.Sprintf("Invalid tag in path: %s", r.URL.Path)
		return "", "", errors.New(err)
	}

	return repoName, tag, nil
}

var (
	listReposRe = regexp.MustCompile(`^/repos/$`)
	repoRe      = regexp.MustCompile(`^/repo/(\S+)$`)
	imageRe     = regexp.MustCompile(`^/image/(\S+)$`)
	tokenRe     = regexp.MustCompile(`^/token(/\S*)?$`)
)

// IRegistryClient is an interface that all registry helpers must implement
type IRegistryClient interface {
	ListRepositories(w http.ResponseWriter, r *http.Request)
	GetRepository(w http.ResponseWriter, r *http.Request)
	GetImage(w http.ResponseWriter, r *http.Request)
	CreateRepository(w http.ResponseWriter, r *http.Request)
	DeleteRepository(w http.ResponseWriter, r *http.Request)
	GetToken(w http.ResponseWriter, r *http.Request)
}

// RegistryServer is http.handler that passes requests to the registry helper implementation
type RegistryServer struct {
	Client IRegistryClient
}

// statusRecorder records the status code from the ResponseWriter
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader records and writes the status code
func (rec *statusRecorder) WriteHeader(statusCode int) {
	rec.statusCode = statusCode
	rec.ResponseWriter.WriteHeader(statusCode)
}

// prometheusMiddleware wraps originalHandler to record the duration of requests
func prometheusMiddleware(originalHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		components := strings.Split(r.URL.Path, "/")
		path := "/"
		if len(components) > 1 {
			path = fmt.Sprintf("/%s", components[1])
		}

		rw := statusRecorder{w, 0}

		originalHandler.ServeHTTP(&rw, r)
		httpDuration.WithLabelValues(r.Method, path, fmt.Sprintf("%d", rw.statusCode)).Observe(time.Since(start).Seconds())
	})
}

// ServeHTTP passes requests to the registry helper implementation
func (h *RegistryServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("content-type", "application/json")
	switch {
	case r.Method == http.MethodGet && listReposRe.MatchString(r.URL.Path):
		h.Client.ListRepositories(w, r)
		return
	case r.Method == http.MethodGet && repoRe.MatchString(r.URL.Path):
		h.Client.GetRepository(w, r)
		return
	case r.Method == http.MethodGet && imageRe.MatchString(r.URL.Path):
		h.Client.GetImage(w, r)
		return
	case r.Method == http.MethodPost && repoRe.MatchString(r.URL.Path):
		h.Client.CreateRepository(w, r)
		return
	case r.Method == http.MethodDelete && repoRe.MatchString(r.URL.Path):
		h.Client.DeleteRepository(w, r)
		return
	case r.Method == http.MethodPost && tokenRe.MatchString(r.URL.Path):
		h.Client.GetToken(w, r)
		return
	default:
		log.Printf("Invalid request: %s %s", r.Method, r.URL.Path)
		NotFound(w, r)
		return
	}
}

// CreateServer configures a new http handler for the registry helper
func CreateServer(mux *http.ServeMux, registryH IRegistryClient, authToken string, promRegistry *prometheus.Registry) {
	serverH := &RegistryServer{
		Client: registryH,
	}
	authorisedH := CheckAuthorised(serverH, authToken)
	h := prometheusMiddleware(authorisedH)

	mux.Handle("/repos/", h)
	mux.Handle("/repo/", h)
	mux.Handle("/image/", h)
	mux.Handle("/token/", h)

	promRegistry.MustRegister(httpDuration)
}
