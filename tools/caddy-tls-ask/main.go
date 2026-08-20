package main

import (
	"net/http"
	"net/netip"
	"os"
	"strings"
)

func check(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	if len(domain) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	ip, err := netip.ParseAddr(domain)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if !ip.IsPrivate() && !ip.IsLoopback() {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func main() {
	var host_port string
	if len(os.Args) > 1 {
		host_port = strings.TrimSpace(os.Args[1])
	} else {
		host_port = "127.0.0.1:50996"
	}

	http.HandleFunc("/", check)

	err := http.ListenAndServe(host_port, nil)
	if err != nil {
		panic(err)
	}
}
