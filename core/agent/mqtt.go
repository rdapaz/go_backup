package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// MqttClient wraps the paho MQTT client for agent communication.
type MqttClient struct {
	client     mqtt.Client
	clientUUID string
	onCommand  func(BackupCommand)
	onSchedule func(ScheduleSync)
}

// NewMqttClient creates a new MQTT client configured for the agent.
func NewMqttClient(brokerAddr string, brokerPort int, username, password, clientUUID string) *MqttClient {
	m := &MqttClient{clientUUID: clientUUID}

	opts := mqtt.NewClientOptions().
		AddBroker(fmt.Sprintf("tcp://%s:%d", brokerAddr, brokerPort)).
		SetClientID(fmt.Sprintf("gobackup-%s", clientUUID[:8])).
		SetUsername(username).
		SetPassword(password).
		SetKeepAlive(30 * time.Second).
		SetAutoReconnect(true).
		SetMaxReconnectInterval(60 * time.Second).
		SetConnectionLostHandler(func(c mqtt.Client, err error) {
			log.Printf("[agent] connection lost: %v", err)
		}).
		SetOnConnectHandler(func(c mqtt.Client) {
			log.Printf("[agent] connected to broker")
			m.subscribe()
		})

	// Set Last Will and Testament
	lwt, _ := Wrap("heartbeat", Heartbeat{Status: "offline"})
	opts.SetWill(TopicHeartbeat(clientUUID), string(lwt), 0, true)

	m.client = mqtt.NewClient(opts)
	return m
}

// Connect establishes the MQTT connection.
func (m *MqttClient) Connect() error {
	token := m.client.Connect()
	token.Wait()
	return token.Error()
}

// Disconnect cleanly disconnects from the broker.
func (m *MqttClient) Disconnect() {
	// Send a final "offline" heartbeat before disconnecting
	m.PublishHeartbeat(Heartbeat{Status: "offline"})
	m.client.Disconnect(1000)
}

// IsConnected returns true if currently connected to the broker.
func (m *MqttClient) IsConnected() bool {
	return m.client.IsConnected()
}

// SetCommandHandler sets the callback for incoming backup commands.
func (m *MqttClient) SetCommandHandler(fn func(BackupCommand)) {
	m.onCommand = fn
}

// SetScheduleHandler sets the callback for incoming schedule syncs.
func (m *MqttClient) SetScheduleHandler(fn func(ScheduleSync)) {
	m.onSchedule = fn
}

func (m *MqttClient) subscribe() {
	// Subscribe to commands for this client
	cmdTopic := TopicCommand(m.clientUUID)
	m.client.Subscribe(cmdTopic, 1, func(c mqtt.Client, msg mqtt.Message) {
		env, err := Unwrap(msg.Payload())
		if err != nil {
			log.Printf("[agent] failed to parse command: %v", err)
			return
		}
		if env.Type == "backup_command" && m.onCommand != nil {
			var cmd BackupCommand
			if err := json.Unmarshal(env.Payload, &cmd); err != nil {
				log.Printf("[agent] failed to unmarshal backup command: %v", err)
				return
			}
			m.onCommand(cmd)
		}
	})

	// Subscribe to schedule sync for this client
	schedTopic := TopicSchedules(m.clientUUID)
	m.client.Subscribe(schedTopic, 1, func(c mqtt.Client, msg mqtt.Message) {
		env, err := Unwrap(msg.Payload())
		if err != nil {
			log.Printf("[agent] failed to parse schedule sync: %v", err)
			return
		}
		if env.Type == "schedule_sync" && m.onSchedule != nil {
			var sync ScheduleSync
			if err := json.Unmarshal(env.Payload, &sync); err != nil {
				log.Printf("[agent] failed to unmarshal schedule sync: %v", err)
				return
			}
			m.onSchedule(sync)
		}
	})
}

// -- Publish methods -------------------------------------------------------------

// PublishRegistration sends a registration request to the orchestrator.
func (m *MqttClient) PublishRegistration(req RegistrationRequest) error {
	data, err := Wrap("register_request", req)
	if err != nil {
		return err
	}
	token := m.client.Publish(TopicRegistrationRequest, 1, false, data)
	token.Wait()
	return token.Error()
}

// PublishHeartbeat sends a heartbeat to the orchestrator.
func (m *MqttClient) PublishHeartbeat(hb Heartbeat) error {
	data, err := Wrap("heartbeat", hb)
	if err != nil {
		return err
	}
	token := m.client.Publish(TopicHeartbeat(m.clientUUID), 0, true, data)
	token.Wait()
	return token.Error()
}

// PublishBackupStatus sends a backup status report to the orchestrator.
func (m *MqttClient) PublishBackupStatus(status BackupStatus) error {
	data, err := Wrap("backup_status", status)
	if err != nil {
		return err
	}
	token := m.client.Publish(TopicStatus(m.clientUUID), 1, false, data)
	token.Wait()
	return token.Error()
}

// SubscribeRegistrationResponse subscribes to registration response for this client.
// The handler is called once when a response arrives.
func (m *MqttClient) SubscribeRegistrationResponse(handler func(RegistrationResponse)) {
	topic := TopicRegistrationResponse(m.clientUUID)
	m.client.Subscribe(topic, 1, func(c mqtt.Client, msg mqtt.Message) {
		env, err := Unwrap(msg.Payload())
		if err != nil {
			return
		}
		if env.Type == "register_response" {
			var resp RegistrationResponse
			if err := json.Unmarshal(env.Payload, &resp); err != nil {
				return
			}
			handler(resp)
		}
	})
}
