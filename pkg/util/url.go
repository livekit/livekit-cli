package util

import (
	"regexp"
)

func ExtractSubdomain(url string) string {
	subdomainPattern := regexp.MustCompile(`^(?:https?|wss?)://([^.]+)\.`)
	matches := subdomainPattern.FindStringSubmatch(url)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}
