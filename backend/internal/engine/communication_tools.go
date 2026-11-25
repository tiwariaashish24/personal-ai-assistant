package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"jarvis/internal/integrations"
)

var sensitiveMessagePattern = regexp.MustCompile(`(?i)\b(?:\d[ -]*?){7,19}\b|\b\d{3}-\d{2}-\d{4}\b`)

func (e *Engine) runGoogleAITool(input, name string, arguments json.RawMessage) (string, error) {
	switch name {
	case "search_gmail":
		var args struct {
			Account *string `json:"account"`
			Query   *string `json:"query"`
			Limit   *int    `json:"limit"`
		}
		if err := decodeToolArguments(arguments, &args); err != nil {
			return "", err
		}
		if args.Account == nil || args.Query == nil {
			return "", errors.New("account and query are required")
		}
		limit := 5
		if args.Limit != nil {
			limit = *args.Limit
			if limit > 10 {
				return "", errors.New("natural-language Gmail searches are limited to 10 messages")
			}
		}
		prompt := fmt.Sprintf("Search Gmail in account %s for %q?", *args.Account, *args.Query)
		return e.authorizeGoogleAction(input, *args.Account, "search_email", "*", prompt, false, func() (string, error) {
			messages, err := e.integrations.SearchEmails(context.Background(), *args.Account, *args.Query, limit)
			if err != nil {
				return "", err
			}
			return encodeToolResult(messages)
		})
	case "read_gmail_message":
		var args struct {
			Account   *string `json:"account"`
			MessageID *string `json:"message_id"`
		}
		if err := decodeToolArguments(arguments, &args); err != nil {
			return "", err
		}
		if args.Account == nil || args.MessageID == nil {
			return "", errors.New("account and message_id are required")
		}
		prompt := fmt.Sprintf("Read Gmail message %s from %s?", *args.MessageID, *args.Account)
		return e.authorizeGoogleAction(input, *args.Account, "read_email", "*", prompt, false, func() (string, error) {
			message, err := e.integrations.ReadEmail(context.Background(), *args.Account, *args.MessageID)
			if err != nil {
				return "", err
			}
			return encodeToolResult(message)
		})
	case "send_gmail":
		var args struct {
			Account    *string  `json:"account"`
			Recipients []string `json:"recipients"`
			Subject    *string  `json:"subject"`
			Body       *string  `json:"body"`
		}
		if err := decodeToolArguments(arguments, &args); err != nil {
			return "", err
		}
		if args.Account == nil || len(args.Recipients) == 0 || args.Subject == nil || args.Body == nil {
			return "", errors.New("account, recipients, subject, and body are required")
		}
		if len(args.Recipients) > 20 {
			return "", errors.New("email is limited to 20 recipients")
		}
		recipients := make([]string, len(args.Recipients))
		for i, value := range args.Recipients {
			address, err := mail.ParseAddress(value)
			if err != nil {
				return "", fmt.Errorf("invalid email recipient: %w", err)
			}
			recipients[i] = strings.ToLower(address.Address)
		}
		forced := len(recipients) > 1 || containsSensitiveContent(*args.Subject+"\n"+*args.Body)
		resource := recipients[0]
		if len(recipients) > 1 {
			resource = "*"
		}
		prompt := fmt.Sprintf("Send email from %s to %s with subject %q?", *args.Account, strings.Join(recipients, ", "), *args.Subject)
		return e.authorizeGoogleAction(input, *args.Account, "send_email", resource, prompt, forced, func() (string, error) {
			id, err := e.integrations.SendEmailTo(context.Background(), *args.Account, recipients, *args.Subject, *args.Body)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Sent email %s to %s.", id, strings.Join(recipients, ", ")), nil
		})
	case "reply_gmail":
		var args struct {
			Account   *string `json:"account"`
			MessageID *string `json:"message_id"`
			Body      *string `json:"body"`
		}
		if err := decodeToolArguments(arguments, &args); err != nil {
			return "", err
		}
		if args.Account == nil || args.MessageID == nil || args.Body == nil {
			return "", errors.New("account, message_id, and body are required")
		}
		if err := e.ensureGoogleReadAllowed(*args.Account); err != nil {
			return "", err
		}
		original, err := e.integrations.ReadEmail(context.Background(), *args.Account, *args.MessageID)
		if err != nil {
			return "", err
		}
		recipient, err := mail.ParseAddress(original.From)
		if err != nil {
			return "", fmt.Errorf("read original sender: %w", err)
		}
		resource := strings.ToLower(recipient.Address)
		prompt := fmt.Sprintf("Reply to %s from %s?", resource, *args.Account)
		return e.authorizeGoogleAction(input, *args.Account, "reply_email", resource, prompt, containsSensitiveContent(*args.Body), func() (string, error) {
			id, err := e.integrations.ReplyEmail(context.Background(), *args.Account, *args.MessageID, *args.Body)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Replied to %s. Message ID: %s.", resource, id), nil
		})
	case "forward_gmail":
		var args struct {
			Account   *string `json:"account"`
			MessageID *string `json:"message_id"`
			Recipient *string `json:"recipient"`
		}
		if err := decodeToolArguments(arguments, &args); err != nil {
			return "", err
		}
		if args.Account == nil || args.MessageID == nil || args.Recipient == nil {
			return "", errors.New("account, message_id, and recipient are required")
		}
		recipient, err := mail.ParseAddress(*args.Recipient)
		if err != nil {
			return "", fmt.Errorf("invalid forward recipient: %w", err)
		}
		if err := e.ensureGoogleReadAllowed(*args.Account); err != nil {
			return "", err
		}
		original, err := e.integrations.ReadEmail(context.Background(), *args.Account, *args.MessageID)
		if err != nil {
			return "", err
		}
		resource := strings.ToLower(recipient.Address)
		prompt := fmt.Sprintf("Forward %q to %s from %s?", original.Subject, resource, *args.Account)
		return e.authorizeGoogleAction(input, *args.Account, "forward_email", resource, prompt, containsSensitiveContent(original.Body), func() (string, error) {
			id, err := e.integrations.ForwardEmail(context.Background(), *args.Account, *args.MessageID, resource)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Forwarded %q to %s. Message ID: %s.", original.Subject, resource, id), nil
		})
	case "archive_gmail", "trash_gmail":
		var args struct {
			Account   *string `json:"account"`
			MessageID *string `json:"message_id"`
		}
		if err := decodeToolArguments(arguments, &args); err != nil {
			return "", err
		}
		if args.Account == nil || args.MessageID == nil {
			return "", errors.New("account and message_id are required")
		}
		action := "archive_email"
		operation := func() (string, error) {
			if err := e.integrations.ArchiveEmail(context.Background(), *args.Account, *args.MessageID); err != nil {
				return "", err
			}
			return "Archived Gmail message " + *args.MessageID + ".", nil
		}
		prompt := fmt.Sprintf("Archive Gmail message %s from %s?", *args.MessageID, *args.Account)
		if name == "trash_gmail" {
			action = "trash_email"
			operation = func() (string, error) {
				if err := e.integrations.TrashEmail(context.Background(), *args.Account, *args.MessageID); err != nil {
					return "", err
				}
				return "Moved Gmail message " + *args.MessageID + " to Trash.", nil
			}
			prompt = fmt.Sprintf("Move Gmail message %s from %s to Trash?", *args.MessageID, *args.Account)
		}
		return e.authorizeGoogleAction(input, *args.Account, action, "*", prompt, action == "trash_email", operation)
	case "download_gmail_attachment":
		var args struct {
			Account     *string `json:"account"`
			MessageID   *string `json:"message_id"`
			Filename    *string `json:"filename"`
			Destination *string `json:"destination"`
		}
		if err := decodeToolArguments(arguments, &args); err != nil {
			return "", err
		}
		if args.Account == nil || args.MessageID == nil || args.Filename == nil {
			return "", errors.New("account, message_id, and filename are required")
		}
		destination := filepath.Base(strings.ReplaceAll(*args.Filename, "\\", "/"))
		if args.Destination != nil && strings.TrimSpace(*args.Destination) != "" {
			destination = *args.Destination
		}
		prompt := fmt.Sprintf("Download attachment %q from Gmail message %s into the JARVIS workspace?", *args.Filename, *args.MessageID)
		return e.authorizeGoogleAction(input, *args.Account, "download_attachment", "*", prompt, false, func() (string, error) {
			attachment, err := e.integrations.DownloadAttachment(context.Background(), *args.Account, *args.MessageID, *args.Filename)
			if err != nil {
				return "", err
			}
			result, err := e.createBytes(destination, attachment.Data)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%s (content type %s).", result.Output, attachment.ContentType), nil
		})
	case "list_google_calendar":
		var args struct {
			Account *string `json:"account"`
			From    *string `json:"from"`
			Until   *string `json:"until"`
		}
		if err := decodeToolArguments(arguments, &args); err != nil {
			return "", err
		}
		if args.Account == nil {
			return "", errors.New("account is required")
		}
		from, until, err := calendarRange(args.From, args.Until)
		if err != nil {
			return "", err
		}
		prompt := fmt.Sprintf("Read Google Calendar events for %s?", *args.Account)
		return e.authorizeGoogleAction(input, *args.Account, "read_calendar", "*", prompt, false, func() (string, error) {
			events, err := e.integrations.ListCalendarEvents(context.Background(), *args.Account, from, until)
			if err != nil {
				return "", err
			}
			return encodeToolResult(events)
		})
	case "create_google_event", "update_google_event":
		var args struct {
			Account     *string `json:"account"`
			EventID     *string `json:"event_id"`
			Summary     *string `json:"summary"`
			Description *string `json:"description"`
			Location    *string `json:"location"`
			Start       *string `json:"start"`
			End         *string `json:"end"`
		}
		if err := decodeToolArguments(arguments, &args); err != nil {
			return "", err
		}
		if args.Account == nil || args.Summary == nil || args.Start == nil || args.End == nil {
			return "", errors.New("account, summary, start, and end are required")
		}
		start, err := time.Parse(time.RFC3339, *args.Start)
		if err != nil {
			return "", errors.New("start must be an RFC3339 timestamp with timezone")
		}
		end, err := time.Parse(time.RFC3339, *args.End)
		if err != nil {
			return "", errors.New("end must be an RFC3339 timestamp with timezone")
		}
		event := integrations.CalendarEvent{
			Summary: *args.Summary,
			Start:   start,
			End:     end,
		}
		if args.EventID != nil {
			event.ID = *args.EventID
		}
		if args.Description != nil {
			event.Description = *args.Description
		}
		if args.Location != nil {
			event.Location = *args.Location
		}
		action := "create_event"
		prompt := fmt.Sprintf("Create Google Calendar event %q from %s to %s?", event.Summary, event.Start.Format(time.RFC3339), event.End.Format(time.RFC3339))
		if name == "update_google_event" {
			if event.ID == "" {
				return "", errors.New("event_id is required")
			}
			action = "update_event"
			prompt = fmt.Sprintf("Update Google Calendar event %q (%s)?", event.Summary, event.ID)
		}
		return e.authorizeGoogleAction(input, *args.Account, action, "*", prompt,
			containsSensitiveContent(event.Summary+"\n"+event.Description+"\n"+event.Location), func() (string, error) {
				var saved integrations.CalendarEvent
				if action == "create_event" {
					saved, err = e.integrations.CreateCalendarEvent(context.Background(), *args.Account, event)
				} else {
					saved, err = e.integrations.UpdateCalendarEvent(context.Background(), *args.Account, event)
				}
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Saved Google Calendar event %q (ID: %s).", saved.Summary, saved.ID), nil
			})
	case "delete_google_event":
		var args struct {
			Account *string `json:"account"`
			EventID *string `json:"event_id"`
		}
		if err := decodeToolArguments(arguments, &args); err != nil {
			return "", err
		}
		if args.Account == nil || args.EventID == nil {
			return "", errors.New("account and event_id are required")
		}
		prompt := fmt.Sprintf("Delete Google Calendar event %s from %s?", *args.EventID, *args.Account)
		return e.authorizeGoogleAction(input, *args.Account, "delete_event", "*", prompt, true, func() (string, error) {
			if err := e.integrations.DeleteCalendarEvent(context.Background(), *args.Account, *args.EventID); err != nil {
				return "", err
			}
			return "Deleted Google Calendar event " + *args.EventID + ".", nil
		})
	default:
		return "", fmt.Errorf("unsupported Google tool %q", name)
	}
}

func (e *Engine) authorizeGoogleAction(
	input, account, action, resource, prompt string,
	forceConfirmation bool,
	operation func() (string, error),
) (string, error) {
	account = strings.ToLower(strings.TrimSpace(account))
	stopped, err := e.integrations.EmergencyStopped()
	if err != nil {
		return "", err
	}
	if stopped {
		return "", e.recordGoogleActionFailure(account, action, resource,
			errors.New("communication emergency stop is active"), "denied")
	}
	decision, err := e.integrations.PermissionFor(integrations.Google, account, action, resource)
	if err != nil {
		return "", err
	}
	if decision == integrations.Deny {
		return "", e.recordGoogleActionFailure(account, action, resource, fmt.Errorf("%s is denied by the saved Google permission policy", action), "denied")
	}

	if action == "send_email" || action == "reply_email" || action == "forward_email" {
		contactDecision, exists, err := e.integrations.AccountPermission(integrations.Google, account, action, resource)
		if err != nil {
			return "", err
		}
		if !exists {
			decision = integrations.Confirm
		} else {
			decision = contactDecision
		}
		if decision == integrations.Deny {
			return "", e.recordGoogleActionFailure(account, action, resource,
				fmt.Errorf("%s to %s is denied by the saved Google permission policy", action, resource), "denied")
		}
	}
	if forceConfirmation || decision == integrations.Confirm {
		return "", e.queueConfirmation(input, prompt, func() (string, error) {
			return e.executeAndAuditGoogleAction(account, action, resource, operation)
		})
	}
	if decision != integrations.Allow {
		return "", fmt.Errorf("%s is not allowed by the saved Google permission policy", action)
	}
	return e.executeAndAuditGoogleAction(account, action, resource, operation)
}

func (e *Engine) executeAndAuditGoogleAction(
	account, action, target string,
	operation func() (string, error),
) (string, error) {
	return e.executeAndAudit(integrations.AuditEvent{
		Provider: integrations.Google,
		Account:  strings.ToLower(account),
		Action:   action,
		Target:   target,
	}, operation)
}

func (e *Engine) executeAndAudit(event integrations.AuditEvent, operation func() (string, error)) (string, error) {
	auditID, err := e.integrations.BeginAudit(event)
	if err != nil {
		return "", fmt.Errorf("operation was not started because its audit entry could not be created: %w", err)
	}
	output, operationErr := operation()
	if operationErr != nil {
		if auditErr := e.integrations.FinishAudit(auditID, "failed"); auditErr != nil {
			return "", errors.Join(operationErr, fmt.Errorf("finalize communication activity: %w", auditErr))
		}
		return "", operationErr
	}
	if err := e.integrations.FinishAudit(auditID, "success"); err != nil {
		return output + "\nWarning: the operation completed, but its audit status could not be finalized.", nil
	}
	return output, nil
}

func (e *Engine) ensureGoogleReadAllowed(account string) error {
	decision, err := e.integrations.PermissionFor(integrations.Google, strings.ToLower(account), "read_email", "*")
	if err != nil {
		return err
	}
	if decision != integrations.Allow {
		return errors.New("reading Gmail is not allowed by the saved integration policy")
	}
	return nil
}

func (e *Engine) recordGoogleActionFailure(account, action, target string, operationErr error, result string) error {
	auditErr := e.integrations.RecordAudit(integrations.AuditEvent{
		Provider: integrations.Google,
		Account:  strings.ToLower(account),
		Action:   action,
		Target:   target,
		Result:   result,
	})
	if auditErr != nil {
		return errors.Join(operationErr, fmt.Errorf("record communication activity: %w", auditErr))
	}
	return operationErr
}

func containsSensitiveContent(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"password", "passcode", "one-time code", "verification code",
		"social security", "ssn", "credit card", "cvv", "routing number",
		"bank account", "account number", "api key", "private key", "recovery code",
		"passport", "date of birth", "tax id", "medical record", "diagnosis",
		"confidential", "private information", "sensitive information",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return sensitiveMessagePattern.MatchString(value)
}

func encodeToolResult(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode communication tool result: %w", err)
	}
	return string(encoded), nil
}

func calendarRange(fromValue, untilValue *string) (time.Time, time.Time, error) {
	var from, until time.Time
	var err error
	if fromValue != nil && strings.TrimSpace(*fromValue) != "" {
		from, err = time.Parse(time.RFC3339, *fromValue)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("from must be an RFC3339 timestamp with timezone")
		}
	}
	if untilValue != nil && strings.TrimSpace(*untilValue) != "" {
		until, err = time.Parse(time.RFC3339, *untilValue)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("until must be an RFC3339 timestamp with timezone")
		}
	}
	return from, until, nil
}
