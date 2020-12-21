package xbmc

// UpNextNotify is used to ask JSONRPC from Python part to send notification to UpNext plugin
func UpNextNotify(payload Args) string {
	var retVal string
	executeJSONRPCEx("UpNext_Notify", &retVal, payload)
	return retVal
}
