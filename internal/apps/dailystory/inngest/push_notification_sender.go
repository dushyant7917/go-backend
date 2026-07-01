package inngest

import (
	"context"
	"fmt"
	"log"
	"time"

	"go-backend/pkg/notification"

	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/step"
)

const (
	BatchNotificationEventName = "dailystory/push_notification.scheduled"
	senderFunctionID           = "dailystory-notification-sender"

	DefaultLanguage = "hi"
)

// NotificationMessages holds localized push notification templates by language code.
// Exported so the cron binary can validate lang codes against the same map.
var NotificationMessages = map[string]struct {
	Title string
	Body  string
}{
	"hi": {
		Title: "आपके लिए नए न्यूज़ पोस्टर्स तैयार हैं!",
		Body:  "नए पोस्टर्स शेयर करें और लोगों के बीच वायरल हो जाएं।",
	},
	"gu": {
		Title: "તમારા માટે નવા ન્યૂઝ પોસ્ટર્સ તૈયાર છે!",
		Body:  "નવા પોસ્ટર્સ શેર કરો અને લોકોમાં વાયરલ થઈ જાઓ.",
	},
	"pa": {
		Title: "ਤੁਹਾਡੇ ਲਈ ਨਵੇਂ ਨਿਊਜ਼ ਪੋਸਟਰ ਤਿਆਰ ਹਨ!",
		Body:  "ਨਵੇਂ ਪੋਸਟਰ ਸ਼ੇਅਰ ਕਰੋ ਅਤੇ ਲੋਕਾਂ ਵਿੱਚ ਵਾਇਰਲ ਹੋ ਜਾਓ।",
	},
	"mr": {
		Title: "तुमच्यासाठी नवीन न्यूज पोस्टर्स तयार आहेत!",
		Body:  "नवीन पोस्टर्स शेअर करा आणि लोकांमध्ये व्हायरल व्हा.",
	},
	"bn": {
		Title: "আপনার জন্য নতুন নিউজ পোস্টার তৈরি!",
		Body:  "নতুন পোস্টার শেয়ার করুন এবং মানুষের মধ্যে ভাইরাল হয়ে যান।",
	},
}

// BatchNotificationEventData is the payload carried by each Inngest batch event.
// Tokens and LangCodes are parallel slices (index i = same user).
type BatchNotificationEventData struct {
	Tokens      []string  `json:"tokens"`
	LangCodes   []string  `json:"lang_codes"`
	ScheduledAt time.Time `json:"scheduled_at"`
	BatchIndex  int       `json:"batch_index"`
}

// BatchNotificationEvent is the typed Inngest event for a single batch of notifications.
type BatchNotificationEvent = inngestgo.GenericEvent[BatchNotificationEventData]

// senderFunc returns the Inngest function body that sleeps until the scheduled time
// then sends a batch of 10 push notifications.
func senderFunc(pushClient *notification.ExpoPushClient) inngestgo.SDKFunction[BatchNotificationEventData] {
	return func(ctx context.Context, input inngestgo.Input[BatchNotificationEventData]) (any, error) {
		data := input.Event.Data

		step.SleepUntil(ctx, "wait-until-send-time", data.ScheduledAt)

		result, err := step.Run(ctx, "send-batch", func(ctx context.Context) (map[string]int64, error) {
			return sendBatch(pushClient, data)
		})
		if err != nil {
			return nil, err
		}

		log.Printf("[inngest] batch %d sent: success=%d failed=%d\n",
			data.BatchIndex, result["success"], result["failed"])
		return result, nil
	}
}

// sendBatch builds ExpoMessages from token+langCode pairs and sends them.
func sendBatch(pushClient *notification.ExpoPushClient, data BatchNotificationEventData) (map[string]int64, error) {
	messages := make([]notification.ExpoMessage, 0, len(data.Tokens))
	for i, token := range data.Tokens {
		langCode := data.LangCodes[i]
		tmpl, ok := NotificationMessages[langCode]
		if !ok {
			tmpl = NotificationMessages[DefaultLanguage]
		}
		messages = append(messages, notification.ExpoMessage{
			To:       token,
			Title:    tmpl.Title,
			Body:     tmpl.Body,
			Data:     map[string]interface{}{"title": tmpl.Title, "body": tmpl.Body},
			Sound:    "default",
			Priority: "high",
		})
	}

	var success, failed int64
	results, err := pushClient.SendBatch(messages)
	if err != nil {
		return nil, fmt.Errorf("SendBatch failed for batch %d: %w", data.BatchIndex, err)
	}
	for _, r := range results {
		if r != nil {
			failed++
		} else {
			success++
		}
	}
	return map[string]int64{"success": success, "failed": failed}, nil
}
