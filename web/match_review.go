// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Web routes for editing match results.

package web

import (
	"fmt"
	"github.com/Team254/cheesy-arena/model"
	"github.com/gorilla/mux"
	"net/http"
	"strconv"
)

type MatchReviewListItem struct {
	Id          int
	DisplayName string
	Time        string
	RedTeams    []int
	BlueTeams   []int
	RedScore    int
	BlueScore   int
	ColorClass  string
}

// Shows the match review interface.
func (web *Web) matchReviewHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsReader(w, r) {
		return
	}

	practiceMatches, err := web.buildMatchReviewList("practice")
	if err != nil {
		handleWebErr(w, err)
		return
	}
	qualificationMatches, err := web.buildMatchReviewList("qualification")
	if err != nil {
		handleWebErr(w, err)
		return
	}
	eliminationMatches, err := web.buildMatchReviewList("elimination")
	if err != nil {
		handleWebErr(w, err)
		return
	}

	template, err := web.parseFiles("templates/match_review.html", "templates/base.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}
	matchesByType := map[string][]MatchReviewListItem{"practice": practiceMatches,
		"qualification": qualificationMatches, "elimination": eliminationMatches}
	if currentMatchType == "" {
		currentMatchType = "practice"
	}
	data := struct {
		*model.EventSettings
		MatchesByType    map[string][]MatchReviewListItem
		CurrentMatchType string
	}{web.arena.EventSettings, matchesByType, currentMatchType}
	err = template.ExecuteTemplate(w, "base", data)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// Shows the page to edit the results for a match.
func (web *Web) matchReviewEditGetHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	match, matchResult, _, err := web.getMatchResultFromRequest(r)
	if err != nil {
		handleWebErr(w, err)
		return
	}

	template, err := web.parseFiles("templates/edit_match_result.html", "templates/base.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}
	matchResultJson, err := matchResult.Serialize()
	if err != nil {
		handleWebErr(w, err)
		return
	}
	data := struct {
		*model.EventSettings
		Match           *model.Match
		MatchResultJson *model.MatchResultDb
	}{web.arena.EventSettings, match, matchResultJson}
	err = template.ExecuteTemplate(w, "base", data)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// Updates the results for a match.
func (web *Web) matchReviewEditPostHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}

	match, matchResult, isCurrent, err := web.getMatchResultFromRequest(r)
	if err != nil {
		handleWebErr(w, err)
		return
	}

	r.ParseForm()
	matchResultJson := model.MatchResultDb{Id: matchResult.Id, MatchId: match.Id, PlayNumber: matchResult.PlayNumber,
		MatchType: matchResult.MatchType, RedScoreJson: r.PostFormValue("redScoreJson"),
		BlueScoreJson: r.PostFormValue("blueScoreJson"), RedCardsJson: r.PostFormValue("redCardsJson"),
		BlueCardsJson: r.PostFormValue("blueCardsJson")}

	// Deserialize the JSON using the same mechanism as to store scoring information in the database.
	matchResult, err = matchResultJson.Deserialize()
	if err != nil {
		handleWebErr(w, err)
		return
	}

	if isCurrent {
		// If editing the current match, just save it back to memory.
		web.arena.RedRealtimeScore.CurrentScore = *matchResult.RedScore
		web.arena.BlueRealtimeScore.CurrentScore = *matchResult.BlueScore
		web.arena.RedRealtimeScore.Cards = matchResult.RedCards
		web.arena.BlueRealtimeScore.Cards = matchResult.BlueCards

		http.Redirect(w, r, "/match_play", 303)
	} else {
		err = web.commitMatchScore(match, matchResult, false)
		if err != nil {
			handleWebErr(w, err)
			return
		}

		http.Redirect(w, r, "/match_review", 303)
	}
}

// Load the match result for the match referenced in the HTTP query string.
func (web *Web) getMatchResultFromRequest(r *http.Request) (*model.Match, *model.MatchResult, bool, error) {
	vars := mux.Vars(r)

	// If editing the current match, get it from memory instead of the DB.
	if vars["matchId"] == "current" {
		return web.arena.CurrentMatch, web.getCurrentMatchResult(), true, nil
	}

	matchId, _ := strconv.Atoi(vars["matchId"])
	match, err := web.arena.Database.GetMatchById(matchId)
	if err != nil {
		return nil, nil, false, err
	}
	if match == nil {
		return nil, nil, false, fmt.Errorf("Error: No such match: %d", matchId)
	}
	matchResult, err := web.arena.Database.GetMatchResultForMatch(matchId)
	if err != nil {
		return nil, nil, false, err
	}
	if matchResult == nil {
		// We're scoring a match that hasn't been played yet, but that's okay.
		matchResult = model.NewMatchResult()
		matchResult.MatchType = match.Type
	}

	return match, matchResult, false, nil
}

// Constructs the list of matches to display in the match review interface.
func (web *Web) buildMatchReviewList(matchType string) ([]MatchReviewListItem, error) {
	matches, err := web.arena.Database.GetMatchesByType(matchType)
	if err != nil {
		return []MatchReviewListItem{}, err
	}

	prefix := ""
	if matchType == "practice" {
		prefix = "P"
	} else if matchType == "qualification" {
		prefix = "Q"
	}
	matchReviewList := make([]MatchReviewListItem, len(matches))
	for i, match := range matches {
		matchReviewList[i].Id = match.Id
		matchReviewList[i].DisplayName = prefix + match.DisplayName
		matchReviewList[i].Time = match.Time.Local().Format("Mon 1/02 03:04 PM")
		matchReviewList[i].RedTeams = []int{match.Red1, match.Red2, match.Red3}
		matchReviewList[i].BlueTeams = []int{match.Blue1, match.Blue2, match.Blue3}
		matchResult, err := web.arena.Database.GetMatchResultForMatch(match.Id)
		if err != nil {
			return []MatchReviewListItem{}, err
		}
		if matchResult != nil {
			matchReviewList[i].RedScore = matchResult.RedScoreSummary().Score
			matchReviewList[i].BlueScore = matchResult.BlueScoreSummary().Score
		}
		switch match.Winner {
		case "R":
			matchReviewList[i].ColorClass = "danger"
		case "B":
			matchReviewList[i].ColorClass = "info"
		case "T":
			matchReviewList[i].ColorClass = "warning"
		default:
			matchReviewList[i].ColorClass = ""
		}
	}

	return matchReviewList, nil
}
