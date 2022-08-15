package util

import (
	"net/http"
	"strings"
)

func RemoveFormatFromString(input string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(input, "  ", ""), "\n", ""), "\t", "")
}

func Delete_empty(s []string) []string {
	var r []string
	for _, str := range s {
		if str != "" {
			r = append(r, str)
		}
	}
	return r
}

func EnableCors(w *http.ResponseWriter) {
	(*w).Header().Set("Access-Control-Allow-Origin", "*")
}
