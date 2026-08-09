package secrets

import "encoding/base64"

func stdB64Decode(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// urlB64Decode handles the URL-safe alphabet (- and _ instead of + and /).
func urlB64Decode(s string) (string, error) {
	b, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
