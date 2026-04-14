package kube

func CommonLabels(engramName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name": "mcp-adapter-engram",
		"bubustack.io/engram":    engramName,
		"mcp.bubustack.io/type":  "server",
	}
}
