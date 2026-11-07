// spike-server serves the verification web UI. It refuses to bind on any
// non-loopback address (enforced in code, not just documented).
package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
)

//go:embed web
var webFiles embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:8090", "listen address (loopback only)")
	flag.Parse()

	if err := requireLoopback(*addr); err != nil {
		log.Fatal(err)
	}

	sub, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("spike web UI: http://%s (loopback only)", *addr)
	log.Fatal(http.Serve(ln, noStore(http.FileServer(http.FS(sub)))))
}

func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "" {
		return logFatalMsg("refusing wildcard bind %q: use an explicit loopback address", addr)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return logFatalMsg("refusing non-loopback bind %q (spike server is loopback-only)", addr)
	}
	return nil
}

func logFatalMsg(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

// noStore keeps the manifest/JS fresh during iteration.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
