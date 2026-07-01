package inngest

import (
	"context"
	"log"
	"time"

	"go-backend/pkg/notification"

	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/step"
)

const (
	WhatsAppMessageEventName = "dailystory/whatsapp_message.scheduled"
	whatsappSenderFunctionID = "dailystory-whatsapp-sender"
)

// WhatsAppMessageEventData is template-agnostic — the full WhatsApp template is
// embedded in the event so any caller can schedule any template through this function.
type WhatsAppMessageEventData struct {
	Phone       string                        `json:"phone"`        // "91XXXXXXXXXX"
	Template    notification.WhatsAppTemplate `json:"template"`
	ScheduledAt time.Time                     `json:"scheduled_at"`
}

// WhatsAppMessageEvent is the typed Inngest event for a single WhatsApp message.
type WhatsAppMessageEvent = inngestgo.GenericEvent[WhatsAppMessageEventData]

// whatsappSenderFunc returns the Inngest function that sleeps until the scheduled
// time then sends the WhatsApp template message. It is completely decoupled from
// template structure — callers embed the full template in the event data.
func whatsappSenderFunc(waClient *notification.WhatsAppClient) inngestgo.SDKFunction[WhatsAppMessageEventData] {
	return func(ctx context.Context, input inngestgo.Input[WhatsAppMessageEventData]) (any, error) {
		data := input.Event.Data

		step.SleepUntil(ctx, "wait-until-send-time", data.ScheduledAt)

		_, err := step.Run(ctx, "send-whatsapp", func(ctx context.Context) (any, error) {
			return nil, waClient.SendTemplate(data.Phone, data.Template)
		})
		if err != nil {
			return nil, err
		}

		log.Printf("[inngest] whatsapp sent template=%s\n", data.Template.Name)
		return nil, nil
	}
}
