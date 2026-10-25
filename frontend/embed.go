// Package frontend owns Mukthish's embedded templates and plain JavaScript assets.
package frontend

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
)

//go:embed templates/*.html assets/*
var content embed.FS

func Handler() (http.Handler, error) {
	page, err := template.ParseFS(content, "templates/*.html")
	if err != nil {
		return nil, err
	}
	assets, err := fs.Sub(content, "assets")
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(assets)))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = page.ExecuteTemplate(w, "products.html", nil)
	})
	mux.HandleFunc("GET /operator/sales", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = page.ExecuteTemplate(w, "sales.html", nil)
	})
	for path, name := range map[string]string{"/checkout": "checkout.html", "/orders": "order.html"} {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = page.ExecuteTemplate(w, name, nil)
		})
	}
	return mux, nil
}
