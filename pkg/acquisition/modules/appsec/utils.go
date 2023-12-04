package appsecacquisition

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/crowdsecurity/coraza/v3/collection"
	"github.com/crowdsecurity/coraza/v3/types/variables"
	"github.com/crowdsecurity/crowdsec/pkg/appsec"
	"github.com/crowdsecurity/crowdsec/pkg/models"
	"github.com/crowdsecurity/crowdsec/pkg/types"
	"github.com/crowdsecurity/go-cs-lib/ptr"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func AppsecEventGeneration(inEvt types.Event) (*types.Event, error) {
	//if the request didnd't trigger inband rules, we don't want to generate an event to LAPI/CAPI
	if !inEvt.Waap.HasInBandMatches {
		return nil, nil
	}
	evt := types.Event{}
	evt.Type = types.APPSEC
	evt.Process = true
	source := models.Source{
		Value: ptr.Of(inEvt.Parsed["source_ip"]),
		IP:    inEvt.Parsed["source_ip"],
		Scope: ptr.Of(types.Ip),
	}

	evt.Overflow.Sources = make(map[string]models.Source)
	evt.Overflow.Sources["ip"] = source

	alert := models.Alert{}
	alert.Capacity = ptr.Of(int32(1))
	alert.Events = make([]*models.Event, 0) //@tko -> URI, method, UA, param name
	alert.Meta = make(models.Meta, 0)       //@tko -> URI, method, UA, param name
	for _, key := range []string{"target_uri", "method"} {

		valueByte, err := json.Marshal([]string{inEvt.Parsed[key]})
		if err != nil {
			log.Debugf("unable to serialize key %s", key)
			continue
		}

		meta := models.MetaItems0{
			Key:   key,
			Value: string(valueByte),
		}
		alert.Meta = append(alert.Meta, &meta)
	}
	alert.EventsCount = ptr.Of(int32(1))
	alert.Labels = []string{"appsec"} //don't know what to do about this
	alert.Leakspeed = ptr.Of("")
	msg := fmt.Sprintf("Application Security Engine alert: %s", inEvt.Waap.MatchedRules.GetName())
	alert.Message = &msg
	alert.Scenario = ptr.Of(inEvt.Waap.MatchedRules.GetName())           // @sbl : should we be able to do inEvt.Waap.MatchedRules.GetHash()
	alert.ScenarioHash = ptr.Of(inEvt.Waap.MatchedRules.GetHash())       // @sbl : should we be able to do inEvt.Waap.MatchedRules.GetHash()
	alert.ScenarioVersion = ptr.Of(inEvt.Waap.MatchedRules.GetVersion()) // @sbl : should we be able to do inEvt.Waap.MatchedRules.GetVersion()
	alert.Simulated = ptr.Of(false)
	alert.Source = &source
	alert.StartAt = ptr.Of(time.Now().UTC().Format(time.RFC3339))
	alert.StopAt = ptr.Of(time.Now().UTC().Format(time.RFC3339))

	evt.Overflow.APIAlerts = []models.Alert{alert}
	evt.Overflow.Alert = &alert
	return &evt, nil
}

func EventFromRequest(r *appsec.ParsedRequest) (types.Event, error) {
	evt := types.Event{}
	//we might want to change this based on in-band vs out-of-band ?
	evt.Type = types.LOG
	evt.ExpectMode = types.LIVE
	//def needs fixing
	evt.Stage = "s00-raw"
	evt.Parsed = map[string]string{
		"source_ip":   r.ClientIP,
		"target_host": r.Host,
		"target_uri":  r.URI,
		"method":      r.Method,
		"req_uuid":    r.Tx.ID(),
		"source":      "crowdsec-waap",

		//TBD:
		//http_status
		//user_agent

	}
	evt.Line = types.Line{
		Time: time.Now(),
		//should we add some info like listen addr/port/path ?
		Labels:  map[string]string{"type": "crowdsec-appsec"}, //FIXME: use the labels from the acquis
		Process: true,
		Module:  "appsec",
		Src:     "appsec",
		Raw:     "dummy-appsec-data", //we discard empty Line.Raw items :)
	}
	evt.Waap = types.AppsecEvent{}

	return evt, nil
}

func LogAppsecEvent(evt *types.Event, logger *log.Entry) {
	req := evt.Parsed["target_uri"]
	if len(req) > 12 {
		req = req[:10] + ".."
	}

	if evt.Meta["waap_interrupted"] == "true" {
		logger.WithFields(log.Fields{
			"module":     "appsec",
			"source":     evt.Parsed["source_ip"],
			"target_uri": req,
		}).Infof("%s blocked on %s (%d rules) [%v]", evt.Parsed["source_ip"], req, len(evt.Waap.MatchedRules), evt.Waap.GetRuleIDs())
	} else if evt.Parsed["outofband_interrupted"] == "true" {
		logger.WithFields(log.Fields{
			"module":     "appsec",
			"source":     evt.Parsed["source_ip"],
			"target_uri": req,
		}).Infof("%s out-of-band blocking rules on %s (%d rules) [%v]", evt.Parsed["source_ip"], req, len(evt.Waap.MatchedRules), evt.Waap.GetRuleIDs())
	} else {
		logger.WithFields(log.Fields{
			"module":     "appsec",
			"source":     evt.Parsed["source_ip"],
			"target_uri": req,
		}).Debugf("%s triggered non-blocking rules on %s (%d rules) [%v]", evt.Parsed["source_ip"], req, len(evt.Waap.MatchedRules), evt.Waap.GetRuleIDs())
	}

}

func (r *AppsecRunner) AccumulateTxToEvent(evt *types.Event, req *appsec.ParsedRequest) error {

	if evt == nil {
		//an error was already emitted, let's not spam the logs
		return nil
	}

	if !req.Tx.IsInterrupted() {
		//if the phase didn't generate an interruption, we don't have anything to add to the event
		return nil
	}
	//if one interruption was generated, event is good for processing :)
	evt.Process = true

	if evt.Meta == nil {
		evt.Meta = map[string]string{}
	}
	if evt.Parsed == nil {
		evt.Parsed = map[string]string{}
	}
	if req.IsInBand {
		evt.Meta["waap_interrupted"] = "true"
		evt.Meta["waap_action"] = req.Tx.Interruption().Action
		evt.Parsed["inband_interrupted"] = "true"
		evt.Parsed["inband_action"] = req.Tx.Interruption().Action
	} else {
		evt.Parsed["outofband_interrupted"] = "true"
		evt.Parsed["outofband_action"] = req.Tx.Interruption().Action
	}

	if evt.Waap.Vars == nil {
		evt.Waap.Vars = map[string]string{}
	}

	req.Tx.Variables().All(func(v variables.RuleVariable, col collection.Collection) bool {
		for _, variable := range col.FindAll() {
			key := ""
			if variable.Key() == "" {
				key = variable.Variable().Name()
			} else {
				key = variable.Variable().Name() + "." + variable.Key()
			}
			if variable.Value() == "" {
				continue
			}
			for _, collectionToKeep := range r.AppsecRuntime.CompiledVariablesTracking {
				match := collectionToKeep.MatchString(key)
				if match {
					evt.Waap.Vars[key] = variable.Value()
					r.logger.Debugf("%s.%s = %s", variable.Variable().Name(), variable.Key(), variable.Value())
				} else {
					r.logger.Debugf("%s.%s != %s (%s) (not kept)", variable.Variable().Name(), variable.Key(), collectionToKeep, variable.Value())
				}
			}
		}
		return true
	})

	for _, rule := range req.Tx.MatchedRules() {
		if rule.Message() == "" {
			r.logger.Tracef("discarding rule %d", rule.Rule().ID())
			continue
		}
		kind := "outofband"
		if req.IsInBand {
			kind = "inband"
			evt.Waap.HasInBandMatches = true
		} else {
			evt.Waap.HasOutBandMatches = true
		}

		name := "NOT_SET"
		version := "NOT_SET"
		hash := "NOT_SET"
		ruleNameProm := fmt.Sprintf("%d", rule.Rule().ID())

		if details, ok := appsec.AppsecRulesDetails[rule.Rule().ID()]; ok {
			//Only set them for custom rules, not for rules written in seclang
			name = details.Name
			version = details.Version
			hash = details.Hash
			ruleNameProm = details.Name
			r.logger.Debugf("custom rule for event, setting name: %s, version: %s, hash: %s", name, version, hash)
		}

		AppsecRuleHits.With(prometheus.Labels{"rule_name": ruleNameProm, "type": kind, "source": req.RemoteAddrNormalized, "appsec_engine": req.AppsecEngine}).Inc()

		corazaRule := map[string]interface{}{
			"id":         rule.Rule().ID(),
			"uri":        evt.Parsed["uri"],
			"rule_type":  kind,
			"method":     evt.Parsed["method"],
			"disruptive": rule.Disruptive(),
			"tags":       rule.Rule().Tags(),
			"file":       rule.Rule().File(),
			"file_line":  rule.Rule().Line(),
			"revision":   rule.Rule().Revision(),
			"secmark":    rule.Rule().SecMark(),
			"accuracy":   rule.Rule().Accuracy(),
			"msg":        rule.Message(),
			"severity":   rule.Rule().Severity().String(),
			"name":       name,
			"hash":       hash,
			"version":    version,
		}
		evt.Waap.MatchedRules = append(evt.Waap.MatchedRules, corazaRule)
	}

	return nil

}