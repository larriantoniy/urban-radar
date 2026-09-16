package publisher

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func vkTestClient(body string) VKClient {
	return VKClient{
		Token:   "test-token",
		GroupID: 123,
		HTTP: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != "/method/wall.post" {
				return nil, errors.New("unexpected VK method")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		})},
	}
}

func TestVKPublishParsesWallPostObjectResponse(t *testing.T) {
	client := vkTestClient(`{"response":{"post_id":987}}`)

	id, err := client.Publish(context.Background(), "text", nil)
	if err != nil {
		t.Fatal(err)
	}
	if id != "987" {
		t.Fatalf("post ID = %q, want 987", id)
	}
}

func TestVKAPIErrorResponseIsTyped(t *testing.T) {
	client := vkTestClient(`{"error":{"error_code":15,"error_msg":"access denied"}}`)

	_, err := client.Publish(context.Background(), "text", nil)
	var apiErr *VKAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want VKAPIError", err)
	}
	if apiErr.Code != 15 || apiErr.Message != "access denied" {
		t.Fatalf("API error = %+v", apiErr)
	}
	if err.Error() != "VK_API_ERROR_15" {
		t.Fatalf("safe error = %q", err)
	}
}

func TestVKPublishRejectsMalformedWallPostResponse(t *testing.T) {
	client := vkTestClient(`{"response":{"post_id":"not-a-number"}}`)

	_, err := client.Publish(context.Background(), "text", nil)
	if err == nil || err.Error() != "VK_INVALID_RESPONSE" {
		t.Fatalf("error = %v, want VK_INVALID_RESPONSE", err)
	}
}
