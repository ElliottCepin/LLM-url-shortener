// main.go
package main

import "net/http"

func handler() http.Handler {
	return http.NewServeMux()
}

func main() {}
