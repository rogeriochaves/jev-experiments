// Package filler builds synthetic support-chat transcripts of a target size.
package filler

import (
	"fmt"
	"math/rand/v2"
	"strings"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

var customerLines = []string{
	"Hi, I ordered a standing desk on the 3rd and the tracking page still says label created.",
	"I already sent the invoice number twice, it is INV-20931.",
	"Can you check whether the refund for order 88213 went through? My bank shows nothing.",
	"The app crashes every time I open the statements tab on Android 15.",
	"ok so what do I do now",
	"I tried clearing the cache like you said, same thing happens.",
	"My flight was moved to 6am and nobody told me, I only saw it in the app this morning.",
	"Is there a way to export the data as CSV instead of the PDF report?",
	"The coupon SPRING25 says invalid but the email I got yesterday says it is valid until Friday.",
	"Thanks, that worked. I can see the export now.",
	"Sure, my account email is m.okafor@example.com and the card ends in 4471.",
	"How long does the review usually take?",
	"I would like to cancel the subscription before the next billing date, which is the 21st.",
	"Right. And the double charge, is that being reversed too?",
	"Fine. Please just make a note on the account that I called about this.",
}

var agentLines = []string{
	"Thanks for reaching out. I can see the order in our system, let me check the carrier status for you.",
	"I have found the invoice. It looks like the billing address on it was pulled from an older profile.",
	"The refund was issued on the 9th and can take 5 to 7 business days to appear on your statement.",
	"Sorry about that. Could you tell me which version of the app you are on? You can find it under Settings > About.",
	"Understood. I have escalated this to the payments team and you will get an email once it is reviewed.",
	"That is correct, both charges will be reversed. The second one was a pending authorization and should drop off automatically.",
	"I have added a note to your account and attached this conversation to it.",
	"You can export a CSV from the Reports page by choosing Download > CSV in the top right corner.",
	"I have applied the coupon manually to your basket, the discount should now show at checkout.",
	"Your subscription is now set to cancel at the end of the current period, so you will not be charged on the 21st.",
	"The review usually completes within one business day, though it can take up to three during busy periods.",
	"I have rebooked you on the 10:40 departure at no extra cost and sent the updated itinerary to your email.",
	"Glad to hear it worked. Is there anything else I can help you with today?",
	"I completely understand the frustration. Let me see what I can do to sort this out right now.",
}

// Transcript returns an alternating user/assistant transcript of roughly targetChars characters.
func Transcript(targetChars int, seed uint64) []Message {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	var msgs []Message
	total := 0
	role := "user"
	for total < targetChars {
		var line string
		if role == "user" {
			line = customerLines[rng.IntN(len(customerLines))]
		} else {
			line = agentLines[rng.IntN(len(agentLines))]
		}
		msgs = append(msgs, Message{Role: role, Content: line})
		total += len(line) + 30
		if role == "user" {
			role = "assistant"
		} else {
			role = "user"
		}
	}
	return msgs
}

// Text renders a transcript as "role: content" lines.
func Text(targetChars int, seed uint64) string {
	var b strings.Builder
	for _, m := range Transcript(targetChars, seed) {
		fmt.Fprintf(&b, "%s: %s\n", m.Role, m.Content)
	}
	return b.String()
}
