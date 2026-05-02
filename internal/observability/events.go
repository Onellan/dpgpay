package observability

import (
	"encoding/json"
	"log"
	"time"
)

func LogEvent(event string, fields map[string]any) {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["event"] = event
	fields["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	encoded, err := json.Marshal(fields)
	if err != nil {
		log.Printf("{\"event\":\"event_log_encode_failed\",\"error\":%q}", err.Error())
		return
	}
	log.Print(string(encoded))
}
