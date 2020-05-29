package httputil

import (
	"fmt"
	"io/ioutil"
	"net/http"
)

var (
	DefaultTransport = http.DefaultTransport
)

func ReadFile(url string, token string) ([]byte, error) {
	client := GetClient()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("could not create request: %v", err)
	}

	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not fetch %s: %v", url, err)
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected status code while reading %s: %v", url, res.StatusCode)
	}

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read content at %s: %v", url, err)
	}
	return body, nil
}

func GetClient() *http.Client {
	return &http.Client{Transport: DefaultTransport}
}
