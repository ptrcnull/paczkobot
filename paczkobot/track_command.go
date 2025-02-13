package paczkobot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"sort"

	"github.com/alufers/paczkobot/commondata"
	"github.com/alufers/paczkobot/commonerrors"
	"github.com/alufers/paczkobot/providers"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
)

type TrackCommand struct {
	App *BotApp
}

func (t *TrackCommand) Usage() string {
	return "/track <shipmentNumber>"
}

func (t *TrackCommand) Help() string {
	return "shows up-to-date tracking information about a package with the given number"
}

type providerReply struct {
	provider providers.Provider
	data     *commondata.TrackingData
	err      error
}

func (t *TrackCommand) Execute(ctx context.Context, args *CommandArguments) error {

	if len(args.Arguments) < 1 {
		return fmt.Errorf("usage: /track &lt;shipmentNumber&gt;")
	}
	shipmentNumber := args.Arguments[0]
	providersToCheck := []providers.Provider{}
	for _, provider := range providers.AllProviders {
		if provider.MatchesNumber(shipmentNumber) {
			providersToCheck = append(providersToCheck, provider)
		}
	}

	if len(providersToCheck) == 0 {
		return fmt.Errorf("no tracking providers support this tracking number")
	}

	statuses := map[string]string{}
	replyChan := make(chan *providerReply, len(providersToCheck))
	for _, p := range providersToCheck {
		statuses[p.GetName()] = "⌛ checking..."
		go func(p providers.Provider) {
			d, err := t.App.TrackingService.InvokeProviderAndNotifyFollowers(context.Background(), p, shipmentNumber)
			if err != nil {
				replyChan <- &providerReply{
					provider: p,
					err:      err,
				}
			} else {
				replyChan <- &providerReply{
					provider: p,
					data:     d,
				}
			}
		}(p)
	}
	var msgIdToEdit int
	sendStatuses := func() {
		var msgText string
		statusesKeys := []string{}
		for k := range statuses {
			statusesKeys = append(statusesKeys, k)
		}
		sort.Strings(statusesKeys)
		for _, n := range statusesKeys {
			v := statuses[n]
			msgText += fmt.Sprintf("%v: <b>%v</b>\n", n, html.EscapeString(v))
		}
		if msgIdToEdit != 0 {
			msg := tgbotapi.NewEditMessageText(args.update.Message.Chat.ID, msgIdToEdit, msgText)
			msg.ParseMode = "HTML"
			_, err := t.App.Bot.Send(msg)
			if err != nil {
				log.Printf("failed to edit status msg: %v", err)
				return
			}
		} else {
			msg := tgbotapi.NewMessage(args.update.Message.Chat.ID, msgText)
			msg.ParseMode = "HTML"
			// msg.ReplyToMessageID = update.Message.MessageID

			res, err := t.App.Bot.Send(msg)
			if err != nil {
				log.Printf("failed to send status msg: %v", err)
				return
			}
			msgIdToEdit = res.MessageID
		}
	}
	sendStatuses()
	for rep := range replyChan {
		if rep.err != nil {
			if errors.Is(rep.err, commonerrors.NotFoundError) {
				statuses[rep.provider.GetName()] = "🔳 Not found"
			} else {
				statuses[rep.provider.GetName()] = "⚠️ Error: " + rep.err.Error()
			}

			sendStatuses()
		} else {
			status := ""
			if len(rep.data.TrackingSteps) > 0 {
				status = rep.data.TrackingSteps[len(rep.data.TrackingSteps)-1].Message
			}
			statuses[rep.provider.GetName()] = "🔎 " + status
			sendStatuses()

			var longTracking = fmt.Sprintf("Detailed tracking for package <i>%v</i> provided by <b>%v</b>:\n", rep.data.ShipmentNumber, rep.data.ProviderName)

			for i, ts := range rep.data.TrackingSteps {
				shouldBold := i == len(rep.data.TrackingSteps)-1
				if shouldBold {
					longTracking += "<b>"
				}
				longTracking += ts.Datetime.Format("2006-01-02 15:04") + " " + ts.Message
				if ts.Location != "" {
					longTracking += " 📌 " + ts.Location
				}
				longTracking += "\n"
				if shouldBold {
					longTracking += "</b>"
				}
			}

			if rep.data.Destination != "" {
				longTracking += "\nThe package is headed to " + rep.data.Destination
			}

			msg := tgbotapi.NewMessage(args.update.Message.Chat.ID, longTracking)
			msg.ParseMode = "HTML"
			msg.ReplyToMessageID = args.update.Message.MessageID
			msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("🚶 Follow this package", fmt.Sprintf("/follow %v", rep.data.ShipmentNumber)),
				),
			)
			t.App.Bot.Send(msg)
		}
	}

	return nil
}
