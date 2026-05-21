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
	seenRuleIDs := make(map[int]struct{}, len(entries))
	for idx, rawEntry := range entries {
		rule, err := normalizeNATRule(rawEntry)
		if err != nil {
			return NATSection{}, newError(CodeNormalizeFailed, fmt.Sprintf("nat.snat.rules[%d]: %v", idx, err), nil)
		}
		if _, exists := seenRuleIDs[rule.RuleID]; exists {
			return NATSection{}, newError(CodeNormalizeFailed, fmt.Sprintf("duplicate nat.snat.rules rule ID %d", rule.RuleID), nil)
		}
		seenRuleIDs[rule.RuleID] = struct{}{}
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
	if err := ValidateInterfaceToken(outbound, "out-interface.name"); err != nil {
		return NATRule{}, err
	}

	sourceAddr, err := readNestedAddress(ruleObj, "source")
	if err != nil {
		return NATRule{}, err
	}
	if err := validateAddressToken(sourceAddr, "source.address"); err != nil {
		return NATRule{}, err
	}

	translationAddr, err := readNestedAddress(ruleObj, "translation")
	if err != nil {
		return NATRule{}, err
	}
	if err := validateAddressToken(translationAddr, "translation.address"); err != nil {
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
	rawRuleID, ok := ruleObj["rule-id"]
	if !ok {
		return 0, fmt.Errorf("missing rule-id")
	}
	id, err := parseJSONInt(rawRuleID)
	if err != nil {
		return 0, fmt.Errorf("invalid rule-id")
	}
	return id, nil
}

func readOutboundInterface(ruleObj map[string]json.RawMessage) (string, error) {
	rawOutbound, ok := ruleObj["out-interface"]
	if !ok {
		return "", fmt.Errorf("missing out-interface")
	}
	name, err := readObjectName(rawOutbound)
	if err != nil {
		return "", fmt.Errorf("invalid out-interface")
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
