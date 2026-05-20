package normalize

import (
	"encoding/json"
	"fmt"
	"sort"
)

func normalizeNAT(root map[string]json.RawMessage) (NATSection, error) {
	rawNAT, ok := root["nat"]
	if !ok {
		return NATSection{}, nil
	}

	var natObject map[string]json.RawMessage
	if err := json.Unmarshal(rawNAT, &natObject); err != nil {
		return NATSection{}, newError(CodeNormalizeFailed, "nat must be an object", err)
	}

	rawSNAT, ok := natObject["snat"]
	if !ok {
		return NATSection{}, nil
	}

	var snatObject map[string]json.RawMessage
	if err := json.Unmarshal(rawSNAT, &snatObject); err != nil {
		return NATSection{}, newError(CodeNormalizeFailed, "nat.snat must be an object", err)
	}

	rawRules, ok := snatObject["rules"]
	if !ok {
		return NATSection{}, nil
	}

	var entries []json.RawMessage
	if err := json.Unmarshal(rawRules, &entries); err != nil {
		return NATSection{}, newError(CodeNormalizeFailed, "nat.snat.rules must be an array", err)
	}

	rules := make([]NATRule, 0, len(entries))
	for idx, rawEntry := range entries {
		rule, err := normalizeNATRule(rawEntry)
		if err != nil {
			return NATSection{}, newError(CodeNormalizeFailed, fmt.Sprintf("nat.snat.rules[%d]: %v", idx, err), nil)
		}
		rules = append(rules, rule)
	}

	sort.Slice(rules, func(i, j int) bool {
		return rules[i].RuleID < rules[j].RuleID
	})

	return NATSection{Rules: rules}, nil
}

func normalizeNATRule(rawRule json.RawMessage) (NATRule, error) {
	var ruleObj map[string]json.RawMessage
	if err := json.Unmarshal(rawRule, &ruleObj); err != nil {
		return NATRule{}, fmt.Errorf("invalid rule object")
	}

	ruleID, err := readRuleID(ruleObj)
	if err != nil {
		return NATRule{}, err
	}

	outbound, err := readOutboundInterface(ruleObj)
	if err != nil {
		return NATRule{}, err
	}

	sourceAddr, err := readNestedAddress(ruleObj, "source")
	if err != nil {
		return NATRule{}, err
	}

	translationAddr, err := readNestedAddress(ruleObj, "translation")
	if err != nil {
		return NATRule{}, err
	}

	return NATRule{
		RuleID:             ruleID,
		OutboundInterface:  outbound,
		SourceAddress:      sourceAddr,
		TranslationAddress: translationAddr,
	}, nil
}

func readRuleID(ruleObj map[string]json.RawMessage) (int, error) {
	rawHyphen, hasHyphen := ruleObj["rule-id"]
	rawSnake, hasSnake := ruleObj["rule_id"]

	if !hasHyphen && !hasSnake {
		return 0, fmt.Errorf("missing rule-id or rule_id")
	}

	if hasHyphen && hasSnake {
		hyp, err := parseJSONInt(rawHyphen)
		if err != nil {
			return 0, fmt.Errorf("invalid rule-id")
		}
		snk, err := parseJSONInt(rawSnake)
		if err != nil {
			return 0, fmt.Errorf("invalid rule_id")
		}
		if hyp != snk {
			return 0, fmt.Errorf("rule-id and rule_id conflict")
		}
		return hyp, nil
	}

	if hasHyphen {
		id, err := parseJSONInt(rawHyphen)
		if err != nil {
			return 0, fmt.Errorf("invalid rule-id")
		}
		return id, nil
	}

	id, err := parseJSONInt(rawSnake)
	if err != nil {
		return 0, fmt.Errorf("invalid rule_id")
	}
	return id, nil
}

func readOutboundInterface(ruleObj map[string]json.RawMessage) (string, error) {
	rawHyphen, hasHyphen := ruleObj["out-interface"]
	rawSnake, hasSnake := ruleObj["out_interface"]

	if !hasHyphen && !hasSnake {
		return "", fmt.Errorf("missing out-interface or out_interface")
	}

	if hasHyphen && hasSnake {
		a, err := readObjectName(rawHyphen)
		if err != nil {
			return "", fmt.Errorf("invalid out-interface")
		}
		b, err := readObjectName(rawSnake)
		if err != nil {
			return "", fmt.Errorf("invalid out_interface")
		}
		if a != b {
			return "", fmt.Errorf("out-interface and out_interface conflict")
		}
		return a, nil
	}

	if hasHyphen {
		name, err := readObjectName(rawHyphen)
		if err != nil {
			return "", fmt.Errorf("invalid out-interface")
		}
		return name, nil
	}

	name, err := readObjectName(rawSnake)
	if err != nil {
		return "", fmt.Errorf("invalid out_interface")
	}
	return name, nil
}

func readNestedAddress(ruleObj map[string]json.RawMessage, key string) (string, error) {
	rawObj, ok := ruleObj[key]
	if !ok {
		return "", fmt.Errorf("missing %s.address", key)
	}
	return readAddress(rawObj)
}

func readAddress(rawObj json.RawMessage) (string, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(rawObj, &obj); err != nil {
		return "", fmt.Errorf("must be an object")
	}

	rawAddr, ok := obj["address"]
	if !ok {
		return "", fmt.Errorf("missing address")
	}

	var addr string
	if err := json.Unmarshal(rawAddr, &addr); err != nil {
		return "", fmt.Errorf("address must be a string")
	}
	if addr == "" {
		return "", fmt.Errorf("address must not be empty")
	}
	return addr, nil
}

func readObjectName(rawObj json.RawMessage) (string, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(rawObj, &obj); err != nil {
		return "", fmt.Errorf("must be an object")
	}

	rawName, ok := obj["name"]
	if !ok {
		return "", fmt.Errorf("missing name")
	}

	var name string
	if err := json.Unmarshal(rawName, &name); err != nil {
		return "", fmt.Errorf("name must be a string")
	}
	if name == "" {
		return "", fmt.Errorf("name must not be empty")
	}
	return name, nil
}

func parseJSONInt(raw json.RawMessage) (int, error) {
	var asInt int
	if err := json.Unmarshal(raw, &asInt); err == nil {
		return asInt, nil
	}
	return 0, fmt.Errorf("must be an integer")
}
