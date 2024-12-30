package util

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
)

func MapStrings(strs []string, fn func(string) string) []string {
	res := make([]string, len(strs))
	for i, str := range strs {
		res[i] = fn(str)
	}
	return res
}

func WrapWith(wrap string) func(string) string {
	return func(str string) string {
		return wrap + str + wrap
	}
}

func EllipsizeTo(str string, maxLength int) string {
	if len(str) <= maxLength {
		return str
	}
	ellipsis := "..."
	contentLen := max(0, min(len(str), maxLength-len(ellipsis)))
	return str[:contentLen] + ellipsis
}

func WrapToLines(input string, maxLineLength int) []string {
	words := strings.Fields(input)
	var lines []string
	var currentLine strings.Builder

	for _, word := range words {
		if currentLine.Len()+len(word)+1 > maxLineLength {
			lines = append(lines, currentLine.String())
			currentLine.Reset()
		}
		if currentLine.Len() > 0 {
			currentLine.WriteString(" ")
		}
		currentLine.WriteString(word)
	}

	if currentLine.Len() > 0 {
		lines = append(lines, currentLine.String())
	}

	return lines
}

func HashString(str string) (string, error) {
	hash := sha256.New()
	if _, err := hash.Write([]byte(str)); err != nil {
		return "", err
	}
	bytes := hash.Sum(nil)
	return hex.EncodeToString(bytes), nil
}

func URLSafeName(projectURL string) (string, error) {
	parsed, err := url.Parse(projectURL)
	if err != nil {
		return "", errors.New("invalid URL")
	}
	subdomain := strings.Split(parsed.Hostname(), ".")[0]
	lastHyphen := strings.LastIndex(subdomain, "-")
	if lastHyphen == -1 {
		return subdomain, nil
	}
	return subdomain[:lastHyphen], nil
}
