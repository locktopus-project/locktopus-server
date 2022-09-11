package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	// internal
	ns "github.com/xshkut/gearlock/internal/namespace"
)

const numberOfPosixSignals = 28

type apiHandler struct {
	version        string
	handler        func(w http.ResponseWriter, r *http.Request)
	connStrExample string
}

func main() {
	parseArguments()

	server := makeServer(hostname, port, apiHandlers)
	listenErr := listen(server)

	exitCode := 0

	select {
	case err := <-listenErr:
		mainLogger.Error(fmt.Errorf("HTTP listener error: %w", err))
		exitCode = 1
	case s := <-getSignals():
		mainLogger.Infof("Received signal: %s", s)
	case <-wait(stopAfter):
		mainLogger.Info("Server TTL exceeded")
	}

	mainLogger.Info("Waiting for existing locks to be released...")
	<-ns.CloseNamespaces()

	mainLogger.Info("Closing HTTP server...")
	server.Close()

	mainLogger.Info("Exit with code", exitCode)
}

func getSignals() <-chan os.Signal {
	ch := make(chan os.Signal, numberOfPosixSignals)

	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)

	return ch
}

func makeServer(hostname string, port string, apiHandlers []apiHandler) http.Server {
	r := mux.NewRouter()

	for _, apiHandler := range apiHandlers {
		r.HandleFunc(apiHandler.version, apiHandler.handler)
	}

	r.HandleFunc("/", greetingsHandler)

	server := http.Server{
		Addr:         fmt.Sprintf("%s:%s", hostname, port),
		WriteTimeout: time.Second * 15,
		ReadTimeout:  time.Second * 15,
		IdleTimeout:  time.Second * 60,
		Handler:      r,
	}

	return server
}

var apiHandlers = []apiHandler{
	{
		version:        "/v1",
		handler:        apiV1Handler,
		connStrExample: "ws://host:port/v1?namespace=default",
	},
	{
		version:        "/stats_v1",
		handler:        statsV1Handler,
		connStrExample: "http://host:port/stats_v1?namespace=default",
	},
}

var lastConnID int64 = -1

func listen(server http.Server) <-chan error {
	r := mux.NewRouter()

	r.HandleFunc("/", greetingsHandler)

	if statInterval > 0 {
		go func() {
			for {
				time.Sleep(time.Duration(statInterval) * time.Second)

				statsList := ns.GetStatistics()

				for _, stats := range statsList {
					mainLogger.Infof("Multilocker namespace: %s. Statistics: %+v", stats.Name, stats.Stats)
				}
			}
		}()
	}

	mainLogger.Info("Starting listening on ", server.Addr)

	ch := make(chan error)

	go func() {
		ch <- fmt.Errorf("HTTP listener error: %w", server.ListenAndServe())
	}()

	return ch
}

func greetingsHandler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("Welcome to GearLock server!\n\nAvailable API versions: \n"))
	for _, apiHandler := range apiHandlers {
		w.Write([]byte(fmt.Sprintf("%s\te.g. %s\n", apiHandler.version, apiHandler.connStrExample)))
	}

	namespaces := ns.GetNamespaces()
	if len(namespaces) == 0 {
		w.Write([]byte("\nNo opened namespaces\n"))
		return
	}

	w.Write([]byte("\nOpened namespaces:\n"))
	for _, ns := range namespaces {
		w.Write([]byte(fmt.Sprintf("%s\n", ns.Name)))
	}
}

func wait(seconds int) <-chan struct{} {
	ch := make(chan struct{})

	if seconds > 0 {
		go func() {
			time.Sleep(time.Duration(seconds) * time.Second)
			close(ch)
		}()
	}

	return ch
}
