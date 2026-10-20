package api42

import (
	"context"
	"testing"
)

// GetPhoto sends the bearer token, so it must refuse any URL off 42's photo CDN.
func TestGetPhotoRejectsOtherHosts(t *testing.T) {
	t.Parallel()
	client := New("token", nil)
	for _, u := range []string{
		"http://cdn.intra.42.fr/users/x/a.jpg",
		"https://example.com/users/x/a.jpg",
		"https://cdn.intra.42.fr.example.com/a.jpg",
		"not a url",
	} {
		if _, err := client.GetPhoto(context.Background(), u); err == nil {
			t.Errorf("GetPhoto(%q) succeeded, want rejection", u)
		}
	}
}
