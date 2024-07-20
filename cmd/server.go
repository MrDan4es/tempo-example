package main

import (
	"fmt"
	"log"
	"net/http"
)

func headers(w http.ResponseWriter, req *http.Request) {

	for name, headers := range req.Header {
		for _, h := range headers {
			fmt.Fprintf(w, "%v: %v\n", name, h)
		}
	}
}

func main() {
	http.HandleFunc("/", headers)

	log.Println("starting http server")

	err := http.ListenAndServe(":80", nil)
	if err != nil {
		panic(err)
	}
}
