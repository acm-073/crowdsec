package hubtest

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	"github.com/crowdsecurity/go-cs-lib/maptools"

	"github.com/crowdsecurity/crowdsec/pkg/appsec/appsec_rule"
	"github.com/crowdsecurity/crowdsec/pkg/cwhub"
)

type Coverage struct {
	Name       string
	TestsCount int
	PresentIn  map[string]bool // poorman's set
}

func (h *HubTest) GetAppsecCoverage(hubDir string) ([]Coverage, error) {
	if len(h.HubIndex.GetItemMap(cwhub.APPSEC_RULES)) == 0 {
		return nil, errors.New("no appsec rules in hub index")
	}

	// populate from hub, iterate in alphabetical order
	pkeys := maptools.SortedKeys(h.HubIndex.GetItemMap(cwhub.APPSEC_RULES))
	coverage := make([]Coverage, len(pkeys))

	for i, name := range pkeys {
		coverage[i] = Coverage{
			Name:       name,
			TestsCount: 0,
			PresentIn:  make(map[string]bool),
		}
	}

	// parser the expressions a-la-oneagain
	appsecTestConfigs, err := filepath.Glob(filepath.Join(hubDir, ".appsec-tests", "*", "config.yaml"))
	if err != nil {
		return nil, fmt.Errorf("while find appsec-tests config: %w", err)
	}

	for _, appsecTestConfigPath := range appsecTestConfigs {
		configFileData := &HubTestItemConfig{}

		yamlFile, err := os.ReadFile(appsecTestConfigPath)
		if err != nil {
			log.Printf("unable to open appsec test config file '%s': %s", appsecTestConfigPath, err)
			continue
		}

		err = yaml.Unmarshal(yamlFile, configFileData)
		if err != nil {
			return nil, fmt.Errorf("parsing: %w", err)
		}

		for _, appsecRulesFile := range configFileData.AppsecRules {
			appsecRuleData := &appsec_rule.CustomRule{}

			yamlFile, err := os.ReadFile(appsecRulesFile)
			if err != nil {
				log.Printf("unable to open appsec rule '%s': %s", appsecRulesFile, err)
			}

			err = yaml.Unmarshal(yamlFile, appsecRuleData)
			if err != nil {
				return nil, fmt.Errorf("parsing: %w", err)
			}

			appsecRuleName := appsecRuleData.Name

			for idx, cov := range coverage {
				if cov.Name == appsecRuleName {
					coverage[idx].TestsCount++
					coverage[idx].PresentIn[appsecTestConfigPath] = true
				}
			}
		}
	}

	return coverage, nil
}

func (h *HubTest) GetParsersCoverage(hubDir string) ([]Coverage, error) {
	if len(h.HubIndex.GetItemMap(cwhub.PARSERS)) == 0 {
		return nil, errors.New("no parsers in hub index")
	}

	// populate from hub, iterate in alphabetical order
	pkeys := maptools.SortedKeys(h.HubIndex.GetItemMap(cwhub.PARSERS))
	coverage := make([]Coverage, len(pkeys))

	for i, name := range pkeys {
		coverage[i] = Coverage{
			Name:       name,
			TestsCount: 0,
			PresentIn:  make(map[string]bool),
		}
	}

	// parser the expressions a-la-oneagain
	passerts, err := filepath.Glob(filepath.Join(hubDir, ".tests", "*", "parser.assert"))
	if err != nil {
		return nil, fmt.Errorf("while find parser asserts: %w", err)
	}

	for _, assert := range passerts {
		file, err := os.Open(assert)
		if err != nil {
			return nil, fmt.Errorf("while reading %s: %w", assert, err)
		}

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			log.Debugf("assert line : %s", line)

			match := parserResultRE.FindStringSubmatch(line)
			if len(match) == 0 {
				log.Debugf("%s doesn't match", line)
				continue
			}

			sidx := parserResultRE.SubexpIndex("parser")
			capturedParser := match[sidx]

			for idx, pcover := range coverage {
				if pcover.Name == capturedParser {
					coverage[idx].TestsCount++
					coverage[idx].PresentIn[assert] = true

					continue
				}

				parserNameSplit := strings.Split(pcover.Name, "/")
				parserNameOnly := parserNameSplit[len(parserNameSplit)-1]

				if parserNameOnly == capturedParser {
					coverage[idx].TestsCount++
					coverage[idx].PresentIn[assert] = true

					continue
				}

				capturedParserSplit := strings.Split(capturedParser, "/")
				capturedParserName := capturedParserSplit[len(capturedParserSplit)-1]

				if capturedParserName == parserNameOnly {
					coverage[idx].TestsCount++
					coverage[idx].PresentIn[assert] = true

					continue
				}

				if capturedParserName == parserNameOnly+"-logs" {
					coverage[idx].TestsCount++
					coverage[idx].PresentIn[assert] = true

					continue
				}
			}
		}

		file.Close()
	}

	return coverage, nil
}

func (h *HubTest) GetScenariosCoverage(hubDir string) ([]Coverage, error) {
	if len(h.HubIndex.GetItemMap(cwhub.SCENARIOS)) == 0 {
		return nil, errors.New("no scenarios in hub index")
	}

	// populate from hub, iterate in alphabetical order
	pkeys := maptools.SortedKeys(h.HubIndex.GetItemMap(cwhub.SCENARIOS))
	coverage := make([]Coverage, len(pkeys))

	for i, name := range pkeys {
		coverage[i] = Coverage{
			Name:       name,
			TestsCount: 0,
			PresentIn:  make(map[string]bool),
		}
	}

	// parser the expressions a-la-oneagain
	passerts, err := filepath.Glob(filepath.Join(hubDir, ".tests", "*", "scenario.assert"))
	if err != nil {
		return nil, fmt.Errorf("while find scenario asserts: %w", err)
	}

	for _, assert := range passerts {
		file, err := os.Open(assert)
		if err != nil {
			return nil, fmt.Errorf("while reading %s: %w", assert, err)
		}

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			log.Debugf("assert line : %s", line)
			match := scenarioResultRE.FindStringSubmatch(line)

			if len(match) == 0 {
				log.Debugf("%s doesn't match", line)
				continue
			}

			sidx := scenarioResultRE.SubexpIndex("scenario")
			scannerName := match[sidx]

			for idx, pcover := range coverage {
				if pcover.Name == scannerName {
					coverage[idx].TestsCount++
					coverage[idx].PresentIn[assert] = true

					continue
				}

				scenarioNameSplit := strings.Split(pcover.Name, "/")
				scenarioNameOnly := scenarioNameSplit[len(scenarioNameSplit)-1]

				if scenarioNameOnly == scannerName {
					coverage[idx].TestsCount++
					coverage[idx].PresentIn[assert] = true

					continue
				}

				fixedProbingWord := strings.ReplaceAll(pcover.Name, "probbing", "probing")
				fixedProbingAssert := strings.ReplaceAll(scannerName, "probbing", "probing")

				if fixedProbingWord == fixedProbingAssert {
					coverage[idx].TestsCount++
					coverage[idx].PresentIn[assert] = true

					continue
				}

				if fmt.Sprintf("%s-detection", pcover.Name) == scannerName {
					coverage[idx].TestsCount++
					coverage[idx].PresentIn[assert] = true

					continue
				}

				if fmt.Sprintf("%s-detection", fixedProbingWord) == fixedProbingAssert {
					coverage[idx].TestsCount++
					coverage[idx].PresentIn[assert] = true

					continue
				}
			}
		}

		file.Close()
	}

	return coverage, nil
}
