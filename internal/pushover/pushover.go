package pushover

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/rs/zerolog"
)

const apiURL = "https://api.pushover.net/1/messages.json"

// Notifier sends push notifications via Pushover.
type Notifier struct {
	token  string
	user   string
	client *http.Client
	log    zerolog.Logger
}

func New(token, user string, log zerolog.Logger) *Notifier {
	return &Notifier{
		token:  token,
		user:   user,
		client: &http.Client{Timeout: 10 * time.Second},
		log:    log,
	}
}

func (n *Notifier) Notify(ctx context.Context, title, message string) {
	resp, err := n.client.PostForm(apiURL, url.Values{
		"token":   {n.token},
		"user":    {n.user},
		"title":   {title},
		"message": {message},
	})
	if err != nil {
		n.log.Warn().Err(err).Msg("failed to send pushover notification")
		return
	}
	resp.Body.Close()
}
