package discord

import "github.com/lucasduport/iptv-proxy/pkg/utils"

type WebhookClient struct {
	enabled bool
}

func NewWebhookClient() *WebhookClient {
	utils.InfoLog("Discord webhooks are not supported; WebhookClient is disabled")
	return &WebhookClient{enabled: false}
}

type WebhookMessage struct{}

type WebhookEmbed struct{}
type WebhookEmbedField struct{}
type WebhookEmbedFooter struct{}

func (w *WebhookClient) SendMessage(string) error                                 { return nil }
func (w *WebhookClient) SendUserConnected(string, string, string) error           { return nil }
func (w *WebhookClient) SendUserDisconnected(string, string, interface{}) error   { return nil }
func (w *WebhookClient) SendSystemStatus(int, int, map[string]interface{}) error  { return nil }
func (w *WebhookClient) SendError(string, string, string) error                   { return nil }
func (w *WebhookClient) SendVODRequest(string, string, string) error              { return nil }
