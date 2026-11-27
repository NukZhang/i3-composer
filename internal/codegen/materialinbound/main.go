package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const unitID = "material-inbound"

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	env := os.Getenv("ENV")
	if env == "" {
		env = "test"
	}
	root := os.Getenv("ARTIFACT_DIR")
	if root == "" {
		root = ".i3-artifacts"
	}
	// 不同单元和环境各用一份业务账本，同一环境换版本后继续重放。
	path := filepath.Join(root, "material-inbound", filepath.Base(unitID), filepath.Base(env), "receipts.json")
	h, err := newHandler(path, env, os.Getenv("COMMIT"))
	if err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("listen=%s", ln.Addr())
	s := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(s.Serve(ln))
}
