package waf

import (
	"os"

	"github.com/crowdsecurity/crowdsec/pkg/cwhub"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

var waapRules map[string]WaapCollectionConfig = make(map[string]WaapCollectionConfig) //FIXME: would probably be better to have a struct for this

var hub *cwhub.Hub //FIXME: this is a temporary hack to make the hub available in the package

func LoadWaapRules(hubInstance *cwhub.Hub) error {

	hub = hubInstance

	for _, hubWafRuleItem := range hub.GetItemMap(cwhub.WAAP_RULES) {
		//log.Infof("loading %s", hubWafRuleItem.LocalPath)
		if !hubWafRuleItem.State.Installed {
			continue
		}

		content, err := os.ReadFile(hubWafRuleItem.State.LocalPath)

		if err != nil {
			log.Warnf("unable to read file %s : %s", hubWafRuleItem.State.LocalPath, err)
			continue
		}

		var rule WaapCollectionConfig

		err = yaml.UnmarshalStrict(content, &rule)

		if err != nil {
			log.Warnf("unable to unmarshal file %s : %s", hubWafRuleItem.State.LocalPath, err)
			continue
		}

		if rule.Type != WAAP_RULE {
			log.Warnf("unexpected type %s instead of %s for file %s", rule.Type, WAAP_RULE, hubWafRuleItem.State.LocalPath)
			continue
		}

		rule.hash = hubWafRuleItem.State.LocalHash
		rule.version = hubWafRuleItem.Version

		log.Infof("Adding %s to waap rules", rule.Name)

		waapRules[rule.Name] = rule
	}

	if len(waapRules) == 0 {
		log.Debugf("No waap rules found")
	}
	return nil
}