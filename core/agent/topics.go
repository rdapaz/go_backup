package agent

import "fmt"

const (
	TopicPrefix = "backup"

	// Client -> Orchestrator: registration request
	TopicRegistrationRequest = TopicPrefix + "/registration/request"

	// Orchestrator -> Client: heartbeat wildcard (orchestrator subscribes)
	TopicHeartbeatWildcard = TopicPrefix + "/heartbeat/+"

	// Client -> Orchestrator: status wildcard (orchestrator subscribes)
	TopicStatusWildcard = TopicPrefix + "/status/+"
)

// TopicRegistrationResponse returns the registration response topic for a client.
func TopicRegistrationResponse(clientUUID string) string {
	return fmt.Sprintf("%s/registration/response/%s", TopicPrefix, clientUUID)
}

// TopicHeartbeat returns the heartbeat topic for a client.
func TopicHeartbeat(clientUUID string) string {
	return fmt.Sprintf("%s/heartbeat/%s", TopicPrefix, clientUUID)
}

// TopicCommand returns the command topic for a client.
func TopicCommand(clientUUID string) string {
	return fmt.Sprintf("%s/command/%s", TopicPrefix, clientUUID)
}

// TopicStatus returns the status topic for a client.
func TopicStatus(clientUUID string) string {
	return fmt.Sprintf("%s/status/%s", TopicPrefix, clientUUID)
}

// TopicSchedules returns the schedule sync topic for a client.
func TopicSchedules(clientUUID string) string {
	return fmt.Sprintf("%s/schedules/%s", TopicPrefix, clientUUID)
}
