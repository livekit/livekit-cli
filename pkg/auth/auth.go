package auth

import (
	"net/http"
)

func NewHeaderWithToken(token string) http.Header {
	header := make(http.Header)
	header.Set("Authorization", "Bearer "+token)
	return header
}
