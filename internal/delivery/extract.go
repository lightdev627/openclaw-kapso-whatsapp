package delivery

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/Enriquefft/openclaw-kapso-whatsapp/internal/kapso"
	"github.com/Enriquefft/openclaw-kapso-whatsapp/internal/transcribe"
)

// ExtractText converts an inbound message of any supported type into a text
// representation suitable for forwarding to the gateway. It returns the text
// and true on success, or ("", false) if the message should be skipped.
// Unsupported types are logged and trigger a WhatsApp reply to the sender.
//
// Audio transcription priority:
//  1. Server-side transcript from Kapso (msg.Kapso.Transcript.Text)
//  2. Local transcription via tr (download from msg.Kapso.MediaURL)
//  3. Fallback to "[audio] (mime)" format
func ExtractText(msg kapso.Message, client *kapso.Client, tr transcribe.Transcriber, maxAudioSize int64) (string, bool) {
	switch msg.Type {
	case "text":
		if msg.Text == nil {
			return "", false
		}
		return msg.Text.Body, true

	case "image":
		if msg.Image == nil {
			return "", false
		}
		return formatMediaMessage("image", msg.Image.Caption, msg.Image.MimeType, msg.Kapso), true

	case "document":
		if msg.Document == nil {
			return "", false
		}
		label := msg.Document.Filename
		if label == "" {
			label = msg.Document.Caption
		}
		return formatMediaMessage("document", label, msg.Document.MimeType, msg.Kapso), true

	case "audio":
		if msg.Audio == nil {
			return "", false
		}
		// 1. Use server-side transcript from Kapso if available.
		if msg.Kapso != nil && msg.Kapso.Transcript != nil && msg.Kapso.Transcript.Text != "" {
			return "[voice] " + msg.Kapso.Transcript.Text, true
		}
		// 2. Local transcription via configured transcriber.
		if tr != nil {
			mediaURL := kapsoMediaURL(msg.Kapso)
			if mediaURL != "" {
				if audio, err := client.DownloadMedia(mediaURL, maxAudioSize); err == nil {
					if text, err := tr.Transcribe(context.Background(), audio, msg.Audio.MimeType); err == nil {
						return "[voice] " + text, true
					} else {
						log.Printf("WARN: transcription failed for message %s: %v", msg.ID, err)
					}
				} else {
					log.Printf("WARN: audio download failed for message %s: %v", msg.ID, err)
				}
			} else {
				log.Printf("WARN: no media URL available for audio message %s", msg.ID)
			}
		}
		// 3. Fallback.
		return formatMediaMessage("audio", "", msg.Audio.MimeType, msg.Kapso), true

	case "video":
		if msg.Video == nil {
			return "", false
		}
		return formatMediaMessage("video", msg.Video.Caption, msg.Video.MimeType, msg.Kapso), true

	case "location":
		if msg.Location == nil {
			return "", false
		}
		return formatLocationMessage(msg.Location), true

	default:
		log.Printf("unsupported message type %q from %s (id=%s)", msg.Type, msg.From, msg.ID)
		go notifyUnsupported(msg.From, msg.Type, client)
		return "", false
	}
}

// kapsoMediaURL returns the media URL from KapsoMeta, or "" if unavailable.
func kapsoMediaURL(k *kapso.KapsoMeta) string {
	if k == nil {
		return ""
	}
	return k.MediaURL
}

// formatMediaMessage builds a text representation for a media attachment.
// It uses the media URL from Kapso enrichment when available.
func formatMediaMessage(kind, label, mimeType string, k *kapso.KapsoMeta) string {
	var parts []string
	parts = append(parts, "["+kind+"]")
	if label != "" {
		parts = append(parts, label)
	}
	if mimeType != "" {
		parts = append(parts, "("+mimeType+")")
	}

	if url := kapsoMediaURL(k); url != "" {
		parts = append(parts, url)
	}

	return strings.Join(parts, " ")
}

// formatLocationMessage builds a text representation for a location message.
func formatLocationMessage(loc *kapso.LocationContent) string {
	var parts []string
	parts = append(parts, "[location]")
	if loc.Name != "" {
		parts = append(parts, loc.Name)
	}
	if loc.Address != "" {
		parts = append(parts, loc.Address)
	}
	parts = append(parts, fmt.Sprintf("(%.6f, %.6f)", loc.Latitude, loc.Longitude))
	return strings.Join(parts, " ")
}

// notifyUnsupported sends a WhatsApp reply informing the user that their
// message type is not yet supported.
func notifyUnsupported(from, msgType string, client *kapso.Client) {
	to := from
	if !strings.HasPrefix(to, "+") {
		to = "+" + to
	}
	reply := fmt.Sprintf("Sorry, I can't process %s messages yet. Please send text instead.", msgType)
	if _, err := client.SendText(to, reply); err != nil {
		log.Printf("failed to send unsupported-type notice to %s: %v", to, err)
	}
}
