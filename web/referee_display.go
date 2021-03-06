// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Web handlers for the referee interface.

package web

import (
	"fmt"
	"github.com/Team254/cheesy-arena/field"
	"github.com/Team254/cheesy-arena/game"
	"github.com/Team254/cheesy-arena/model"
	"github.com/mitchellh/mapstructure"
	"io"
	"log"
	"net/http"
	"strconv"
)

// Renders the referee interface for assigning fouls.
func (web *Web) refereeDisplayHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	template, err := web.parseFiles("templates/referee_display.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}

	match := web.arena.CurrentMatch
	matchType := match.CapitalizedType()
	red1 := web.arena.AllianceStations["R1"].Team
	if red1 == nil {
		red1 = &model.Team{}
	}
	red2 := web.arena.AllianceStations["R2"].Team
	if red2 == nil {
		red2 = &model.Team{}
	}
	red3 := web.arena.AllianceStations["R3"].Team
	if red3 == nil {
		red3 = &model.Team{}
	}
	blue1 := web.arena.AllianceStations["B1"].Team
	if blue1 == nil {
		blue1 = &model.Team{}
	}
	blue2 := web.arena.AllianceStations["B2"].Team
	if blue2 == nil {
		blue2 = &model.Team{}
	}
	blue3 := web.arena.AllianceStations["B3"].Team
	if blue3 == nil {
		blue3 = &model.Team{}
	}
	data := struct {
		*model.EventSettings
		MatchType        string
		MatchDisplayName string
		Red1             *model.Team
		Red2             *model.Team
		Red3             *model.Team
		Blue1            *model.Team
		Blue2            *model.Team
		Blue3            *model.Team
		RedFouls         []game.Foul
		BlueFouls        []game.Foul
		RedCards         map[string]string
		BlueCards        map[string]string
		Rules            []game.Rule
		EntryEnabled     bool
	}{web.arena.EventSettings, matchType, match.DisplayName, red1, red2, red3, blue1, blue2, blue3,
		web.arena.RedRealtimeScore.CurrentScore.Fouls, web.arena.BlueRealtimeScore.CurrentScore.Fouls,
		web.arena.RedRealtimeScore.Cards, web.arena.BlueRealtimeScore.Cards, game.Rules,
		!(web.arena.RedRealtimeScore.FoulsCommitted && web.arena.BlueRealtimeScore.FoulsCommitted)}
	err = template.ExecuteTemplate(w, "referee_display.html", data)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// The websocket endpoint for the refereee interface client to send control commands and receive status updates.
func (web *Web) refereeDisplayWebsocketHandler(w http.ResponseWriter, r *http.Request) {
	// TODO(patrick): Enable authentication once Safari (for iPad) supports it over Websocket.

	websocket, err := NewWebsocket(w, r)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	defer websocket.Close()

	matchLoadTeamsListener := web.arena.MatchLoadTeamsNotifier.Listen()
	defer close(matchLoadTeamsListener)
	reloadDisplaysListener := web.arena.ReloadDisplaysNotifier.Listen()
	defer close(reloadDisplaysListener)

	// Spin off a goroutine to listen for notifications and pass them on through the websocket.
	go func() {
		for {
			var messageType string
			var message interface{}
			select {
			case _, ok := <-matchLoadTeamsListener:
				if !ok {
					return
				}
				messageType = "reload"
				message = nil
			case _, ok := <-reloadDisplaysListener:
				if !ok {
					return
				}
				messageType = "reload"
				message = nil
			}
			err = websocket.Write(messageType, message)
			if err != nil {
				// The client has probably closed the connection; nothing to do here.
				return
			}
		}
	}()

	// Loop, waiting for commands and responding to them, until the client closes the connection.
	for {
		messageType, data, err := websocket.Read()
		if err != nil {
			if err == io.EOF {
				// Client has closed the connection; nothing to do here.
				return
			}
			log.Printf("Websocket error: %s", err)
			return
		}

		switch messageType {
		case "addFoul":
			args := struct {
				Alliance    string
				TeamId      int
				Rule        string
				IsTechnical bool
			}{}
			err = mapstructure.Decode(data, &args)
			if err != nil {
				websocket.WriteError(err.Error())
				continue
			}

			// Add the foul to the correct alliance's list.
			foul := game.Foul{Rule: game.Rule{RuleNumber: args.Rule, IsTechnical: args.IsTechnical},
				TeamId: args.TeamId, TimeInMatchSec: web.arena.MatchTimeSec()}
			if args.Alliance == "red" {
				web.arena.RedRealtimeScore.CurrentScore.Fouls =
					append(web.arena.RedRealtimeScore.CurrentScore.Fouls, foul)
			} else {
				web.arena.BlueRealtimeScore.CurrentScore.Fouls =
					append(web.arena.BlueRealtimeScore.CurrentScore.Fouls, foul)
			}
			web.arena.RealtimeScoreNotifier.Notify(nil)
		case "deleteFoul":
			args := struct {
				Alliance       string
				TeamId         int
				Rule           string
				IsTechnical    bool
				TimeInMatchSec float64
			}{}
			err = mapstructure.Decode(data, &args)
			if err != nil {
				websocket.WriteError(err.Error())
				continue
			}

			// Remove the foul from the correct alliance's list.
			deleteFoul := game.Foul{Rule: game.Rule{RuleNumber: args.Rule, IsTechnical: args.IsTechnical},
				TeamId: args.TeamId, TimeInMatchSec: args.TimeInMatchSec}
			var fouls *[]game.Foul
			if args.Alliance == "red" {
				fouls = &web.arena.RedRealtimeScore.CurrentScore.Fouls
			} else {
				fouls = &web.arena.BlueRealtimeScore.CurrentScore.Fouls
			}
			for i, foul := range *fouls {
				if foul == deleteFoul {
					*fouls = append((*fouls)[:i], (*fouls)[i+1:]...)
					break
				}
			}
			web.arena.RealtimeScoreNotifier.Notify(nil)
		case "card":
			args := struct {
				Alliance string
				TeamId   int
				Card     string
			}{}
			err = mapstructure.Decode(data, &args)
			if err != nil {
				websocket.WriteError(err.Error())
				continue
			}

			// Set the card in the correct alliance's score.
			var cards map[string]string
			if args.Alliance == "red" {
				cards = web.arena.RedRealtimeScore.Cards
			} else {
				cards = web.arena.BlueRealtimeScore.Cards
			}
			cards[strconv.Itoa(args.TeamId)] = args.Card
			continue
		case "signalReset":
			if web.arena.MatchState != field.PostMatch {
				// Don't allow clearing the field until the match is over.
				continue
			}
			web.arena.FieldReset = true
			web.arena.AllianceStationDisplayScreen = "fieldReset"
			web.arena.AllianceStationDisplayNotifier.Notify(nil)
			continue // Don't reload.
		case "commitMatch":
			if web.arena.MatchState != field.PostMatch {
				// Don't allow committing the fouls until the match is over.
				continue
			}
			web.arena.RedRealtimeScore.FoulsCommitted = true
			web.arena.BlueRealtimeScore.FoulsCommitted = true
			web.arena.FieldReset = true
			web.arena.AllianceStationDisplayScreen = "fieldReset"
			web.arena.AllianceStationDisplayNotifier.Notify(nil)
			web.arena.ScoringStatusNotifier.Notify(nil)
		default:
			websocket.WriteError(fmt.Sprintf("Invalid message type '%s'.", messageType))
			continue
		}

		// Force a reload of the client to render the updated foul list.
		err = websocket.Write("reload", nil)
		if err != nil {
			log.Printf("Websocket error: %s", err)
			return
		}
	}
}
