package apply

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func clonePlan(plan Plan) Plan {
	plan.DeleteCommands = cloneStrings(plan.DeleteCommands)
	plan.SetCommands = cloneStrings(plan.SetCommands)
	return plan
}
