package inngest

import (
	"go-backend/pkg/notification"

	"github.com/inngest/inngestgo"
)

// RegisterFunctions creates and registers all Inngest functions with the client.
func RegisterFunctions(client inngestgo.Client, pushClient *notification.ExpoPushClient, waClient *notification.WhatsAppClient) error {
	_, err := inngestgo.CreateFunction(
		client,
		inngestgo.FunctionOpts{ID: senderFunctionID},
		inngestgo.EventTrigger(BatchNotificationEventName, nil),
		senderFunc(pushClient),
	)
	if err != nil {
		return err
	}

	_, err = inngestgo.CreateFunction(
		client,
		inngestgo.FunctionOpts{ID: whatsappSenderFunctionID},
		inngestgo.EventTrigger(WhatsAppMessageEventName, nil),
		whatsappSenderFunc(waClient),
	)
	return err
}
