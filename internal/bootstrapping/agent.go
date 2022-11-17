// Copyright (c) 2022 Contributors to the Eclipse Foundation
//
// See the NOTICE file(s) distributed with this work for additional
// information regarding copyright ownership.
//
// This program and the accompanying materials are made available under the
// terms of the Eclipse Public License 2.0 which is available at
// https://www.eclipse.org/legal/epl-2.0, or the Apache License, Version 2.0
// which is available at https://www.apache.org/licenses/LICENSE-2.0.
//
// SPDX-License-Identifier: EPL-2.0 OR Apache-2.0

package bootstrapping

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash"
	"net/http"
	"os"
	"sync"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/pkg/errors"

	"github.com/eclipse/ditto-clients-golang/model"
	"github.com/eclipse/ditto-clients-golang/protocol"
	"github.com/eclipse/ditto-clients-golang/protocol/things"

	"github.com/eclipse-kanto/suite-connector/config"
	"github.com/eclipse-kanto/suite-connector/connector"
	"github.com/eclipse-kanto/suite-connector/logger"
	"github.com/eclipse-kanto/suite-connector/util"
)

const (
	bsDefinitionName = "Bootstrapping"
	bsDefinition     = "com.bosch.iot.suite:" + bsDefinitionName + ":1.0.0"

	bsFeatureID         = bsDefinitionName
	bsEventRequest      = "request"
	bsOperationResponse = "response"
	bsRequestID         = "requestId"

	bsResponsePath = "/features/" + bsFeatureID + "/inbox/messages/response"

	contentTypeJSON = "application/json"

	statusUnknown = 0
	statusOK      = 1
	statusFailed  = 2

	logFieldCorrelationID = "correlation-id"
)

// ResponseResultFunc receives and handles the response result per bootstrapping request.
type ResponseResultFunc func(requestID string, succeeded bool)

// Agent contains the Bootstrapping Agent system data.
type Agent struct {
	request  *Request
	settings *BootstrapSettings

	ResultFunc ResponseResultFunc

	Logger logger.Logger

	mu sync.Mutex
}

// BootstrapSettings contains all Bootstrapping Agent configuration data.
type BootstrapSettings struct {
	logger.LogSettings

	config.HubConnectionSettings

	PreBootstrapFile   string   `json:"preBootstrapFile"`
	PreBootstrapScript []string `json:"preBootstrapScript"`

	PostBootstrapFile   string   `json:"postBootstrapFile"`
	PostBootstrapScript []string `json:"postBootstrapScript"`

	ProvisioningFile          string `json:"provisioningFile"`
	BootstrapProvisioningFile string `json:"bootstrapProvisioningFile"`

	ChunkSize int `json:"chunkSize"`
}

// Validate validates the bootstrapping settings.
func (c *BootstrapSettings) Validate() error {
	if c.ChunkSize <= 0 {
		return errors.New("positive maximum request chunk size is expected")
	}
	return nil
}

// DefaultSettings returns the default settings.
func DefaultSettings() *BootstrapSettings {
	return &BootstrapSettings{
		HubConnectionSettings: config.HubConnectionSettings{
			Address: "mqtts://mqtt.bosch-iot-hub.com:8883",
			TLSSettings: config.TLSSettings{
				CACert: "iothub.crt",
			},
			AutoProvisioningEnabled: true,
		},
		LogSettings: logger.LogSettings{
			LogFile:      "log/suite-bootstrapping.log",
			LogLevel:     logger.INFO,
			LogFileSize:  2,
			LogFileCount: 5,
		},
		ChunkSize:           45 * 1024,
		ProvisioningFile:    "provisioning.json",
		PreBootstrapScript:  make([]string, 0),
		PostBootstrapScript: make([]string, 0),
	}
}

// Request contains the system data related per bootstrapping request.
type Request struct {
	id       string
	response *Response
	status   int8
}

// Response contains the system data related per bootstrapping request response.
type Response struct {
	file         *os.File
	hasher       hash.Hash
	provisioning string
	chunkIndex   int
}

// RequestData defines the request fields.
type RequestData struct {
	ID    string `json:"requestId"`
	Chunk string `json:"chunk,omitempty"`
	Hash  string `json:"hash,omitempty"`
}

// ResponseData defines the response fields.
type ResponseData struct {
	ID           string `json:"requestId"`
	Chunk        string `json:"chunk,omitempty"`
	Hash         string `json:"hash,omitempty"`
	Provisioning string `json:"provisioning,omitempty"`
	Err          int    `json:"error,omitempty"`
}

// NewAgent creates Bootstrapping Agent with provided settings.
func NewAgent(bs *BootstrapSettings, logger logger.Logger, resultfunc ResponseResultFunc) (*Agent, error) {
	if bs == nil {
		return nil, errors.New("bootstrapping settings are expected")
	}

	if bs.ChunkSize < 1 {
		return nil, errors.New("bootstrapping settings chunk size is expected")
	}

	if len(bs.DeviceID) == 0 {
		return nil, errors.New("bootstrapping settings device ID is expected")
	}
	model.NewNamespacedIDFrom(bs.DeviceID) // validates deviceID

	return &Agent{
		settings:   bs,
		Logger:     logger,
		ResultFunc: resultfunc,
		request:    &Request{},
		mu:         sync.Mutex{},
	}, nil
}

// CreateBootstrappingFeature publishes message to ensure the Bootstrapping feature.
func (a *Agent) CreateBootstrappingFeature(publisher message.Publisher) error {
	cmd := things.NewCommand(model.NewNamespacedIDFrom(a.settings.DeviceID)).
		Feature(bsFeatureID).Modify((&model.Feature{}).
		WithDefinitionFrom(bsDefinition))

	correlationID := watermill.NewUUID()
	data, err := json.Marshal(cmd.Envelope(protocol.WithCorrelationID(correlationID),
		protocol.WithResponseRequired(false)))
	if err == nil {
		err = publisher.Publish("e", message.NewMessage(watermill.NewUUID(), []byte(data)))
	}

	if err != nil {
		return errors.Wrapf(err,
			"unable to publish Bootstrapping modify feature command with topic '%s' and path '%s'",
			cmd.Topic,
			cmd.Path)
	}

	if a.Logger.IsDebugEnabled() {
		a.Logger.Debug("Modify Bootstrapping feature command published", watermill.LogFields{
			logFieldCorrelationID: correlationID,
			"topic":               cmd.Topic,
			"path":                cmd.Path,
		})
	}
	return nil
}

func (a *Agent) setRequestID(requestID string, clean bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if clean {
		a.removePostBootstrapFile()
	}

	if len(requestID) == 0 {
		a.request = nil
	} else {
		a.request = &Request{
			id: requestID,
		}
	}
}

func (a *Agent) requestID() string {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.request == nil {
		return ""
	}

	return a.request.id
}

func (a *Agent) result(requestID string, status int8) {
	if status != statusUnknown && a.ResultFunc != nil {
		a.ResultFunc(requestID, status == statusOK)
	}
}

// PublishRequest publishes Bootstrapping request messages.
func (a *Agent) PublishRequest(requestID string, publisher message.Publisher) error {
	if len(requestID) == 0 {
		return errors.New("request ID is expected")
	}
	if publisher == nil {
		return errors.New("request publisher is expected")
	}

	a.setRequestID(requestID, true)

	payload := RequestData{
		ID: requestID,
	}

	msg := things.NewMessage(model.NewNamespacedIDFrom(a.settings.DeviceID)).
		Feature(bsFeatureID).
		Outbox(bsEventRequest)

	correlationID := watermill.NewUUID()
	headersOpts := []protocol.HeaderOpt{
		protocol.WithCorrelationID(correlationID),
		protocol.WithResponseRequired(false),
		protocol.WithContentType(contentTypeJSON),
	}

	hasConfig, err := execPreScript(a.settings.PreBootstrapScript, a.settings.PreBootstrapFile)
	if err != nil {
		return err
	}

	if !hasConfig {
		// include only the request ID when no prebootstrapping data is available
		return a.publish(publisher, msg.WithPayload(payload), correlationID, requestID, 0, headersOpts...)
	}

	hasher := sha256.New()
	var chunkIndex int
	err = ProcessFileWithChunks(
		a.settings.PreBootstrapFile, a.settings.ChunkSize, func(chunk string, hasNext bool) error {
			payload.Chunk = chunk
			if !hasNext {
				payload.Hash = hex.EncodeToString(hasher.Sum(nil))
			}
			chunkIndex = chunkIndex + 1
			return a.publish(
				publisher, msg.WithPayload(payload), correlationID, requestID, chunkIndex, headersOpts...)
		}, hasher)

	if err != nil {
		return err
	}

	return nil
}

func (a *Agent) publish(
	pub message.Publisher, msg *things.Message, correlationID, id string, index int, h ...protocol.HeaderOpt) error {
	env := msg.Envelope(h...)

	data, err := json.Marshal(env)
	if err == nil {
		err = pub.Publish("e", message.NewMessage(watermill.NewUUID(), []byte(data)))
	}

	if err != nil {
		a.Logger.Error("Unable to publish message", err, watermill.LogFields{
			logFieldCorrelationID: correlationID,
			bsRequestID:           id,
			"chunkIndex":          index,
		})
		return err
	}

	if a.Logger.IsDebugEnabled() {
		a.Logger.Debug("Request message published", watermill.LogFields{
			logFieldCorrelationID: correlationID,
			bsRequestID:           id,
			"chunkIndex":          index,
		})
	}
	return nil
}

// HandleResponse handles Bootstrapping response messages.
func (a *Agent) HandleResponse(inboxMsg *message.Message) ([]*message.Message, error) {
	correlationID, respReq, msgErr, err := a.handleResponseMessage(inboxMsg)

	if a.request != nil {
		status := a.request.status
		if status != statusUnknown {
			id := a.requestID()
			a.setRequestID("", status == statusFailed)
			a.result(id, status)
		}
	}

	if respReq {
		statusMsg := things.NewMessage(model.NewNamespacedIDFrom(a.settings.DeviceID)).
			Feature(bsFeatureID).
			Outbox(bsOperationResponse)

		var env *protocol.Envelope
		if msgErr != nil {
			env = statusMsg.Envelope(
				protocol.WithCorrelationID(correlationID),
				protocol.WithContentType(contentTypeJSON)).
				WithValue(msgErr).WithStatus(msgErr.Status)
		} else {
			env = statusMsg.Envelope(protocol.WithCorrelationID(correlationID)).
				WithStatus(http.StatusNoContent)
		}
		return responseStatusMessage(inboxMsg, env, correlationID)
	}

	return nil, err
}

func (a *Agent) handleResponseMessage(msg *message.Message) (string, bool, *MessageError, error) {
	var rspMsg *protocol.Envelope
	if err := json.Unmarshal(msg.Payload, &rspMsg); err != nil {
		a.Logger.Error("Unexpected message format", err, watermill.LogFields{
			"uuid": msg.UUID,
		})
		return "", false, NewMessageParameterInvalidError(), err
	}

	correlationID := rspMsg.Headers.CorrelationID()
	respReq := rspMsg.Headers.IsResponseRequired()

	if model.NewNamespacedID(rspMsg.Topic.Namespace, rspMsg.Topic.EntityName).String() == a.settings.DeviceID {
		if rspMsg.Path == bsResponsePath {
			payload, err := json.Marshal(rspMsg.Value)
			if err != nil {
				return correlationID, respReq, NewMessageParameterInvalidError(), errors.Wrapf(err, "invalid payload: %v", rspMsg.Value)
			}

			var response ResponseData
			if err := json.Unmarshal(payload, &response); err != nil {
				errData := errors.Wrap(err, "unexpected response data types")
				a.Logger.Error("Unexpected response message is not handled",
					errData,
					watermill.LogFields{
						"uuid":                msg.UUID,
						logFieldCorrelationID: correlationID,
					})
				return correlationID, respReq, NewMessageParameterInvalidError(), errData
			}

			id := a.requestID()
			if id != response.ID {
				return correlationID, respReq,
					NewMessageParameterInvalidError(),
					errors.New("response with unknown request ID is not handled")
			}
			if msgError, err := a.addRequestResponse(response, msg.UUID, correlationID); err != nil {
				a.request.status = statusFailed

				a.Logger.Error("Request responses' handling has finished with error", err, watermill.LogFields{
					"uuid":                msg.UUID,
					logFieldCorrelationID: correlationID,
					bsRequestID:           response.ID,
				})

				return correlationID, respReq, msgError, err
			}
			return correlationID, respReq, nil, nil
		}
		return correlationID, respReq, NewMessageParameterInvalidError(),
			errors.New("response with wrong path is not handled")
	}
	return correlationID, respReq, NewMessageParameterInvalidError(),
		errors.New("response with unknown device ID is not handled")
}

func (a *Agent) addRequestResponse(data ResponseData, msgUID, correlationID string) (*MessageError, error) {
	if a.Logger.IsDebugEnabled() {
		a.Logger.Debug("Response message handling...", watermill.LogFields{
			logFieldCorrelationID: correlationID,
			bsRequestID:           data.ID,
		})
	}

	if data.Err > 0 {
		return nil, errors.Errorf("error code received '%v'", data.Err)
	}

	if a.request.response == nil {
		rsp, err := initResponse(a.settings.PostBootstrapFile)
		if err != nil {
			return NewMessageInternalError(), err
		}
		a.request.response = rsp
	}

	if len(data.Provisioning) > 0 {
		a.request.response.provisioning = data.Provisioning
	}

	if len(data.Chunk) > 0 {
		a.request.response.chunkIndex = a.request.response.chunkIndex + 1
		var err error

		if a.request.response.file == nil {
			err = errors.New("the post-bootstrapping file is not provided")
		} else {
			err = WriteFileChunk(a.request.response.file, a.request.response.hasher, data.Chunk)
		}

		if err != nil {
			return NewMessageInternalError(),
				errors.Wrapf(err, "unable to append response chunk '%v'", a.request.response.chunkIndex)
		}
	}

	if len(data.Hash) > 0 {
		chunksChecksum := hex.EncodeToString(a.request.response.hasher.Sum(nil))
		if chunksChecksum == data.Hash {
			if err := manageBootstrappingData(a.request.response, a.settings); err != nil {
				return NewMessageInternalError(), err
			}
			a.request.status = statusOK
			a.Logger.Info("Request responses' handling has finished with success", watermill.LogFields{
				"uuid":                msgUID,
				logFieldCorrelationID: correlationID,
				bsRequestID:           data.ID,
				"hash":                chunksChecksum,
			})
		} else {
			return NewMessageParameterInvalidError(),
				errors.Errorf("unexpected hash '%s' is received, chunks hash sum is '%s'", data.Hash, chunksChecksum)
		}
	}

	return nil, nil
}

func initResponse(postConfig string) (*Response, error) {
	var cFile *os.File
	var err error
	if len(postConfig) > 0 {
		if cFile, err = CreateFile(postConfig); err != nil {
			return nil, errors.Wrapf(err, "post-bootstrapping file '%s' error", postConfig)
		}
	}
	return &Response{
		file:   cFile,
		hasher: sha256.New(),
	}, nil
}

func manageBootstrappingData(response *Response, fileSettings *BootstrapSettings) error {
	response.file.Close()
	if err := execPostScript(fileSettings.PostBootstrapScript); err != nil {
		return err
	}
	if len(response.provisioning) > 0 {
		if len(fileSettings.BootstrapProvisioningFile) == 0 {
			return errors.New(
				"unable to store the received provisioning JSON data, provisioning file location is not specified")
		}
		return CreateFileWithContent(fileSettings.BootstrapProvisioningFile, response.provisioning)
	}
	return nil
}

func execPreScript(script []string, outputFile string) (bool, error) {
	var execErr error
	if len(script) > 0 {
		execErr = ExecScript(script...)
	}

	if len(outputFile) > 0 {
		info, err := os.Stat(outputFile)
		if os.IsNotExist(err) {
			return false, execErr
		}
		if !info.IsDir() && info.Size() > 0 {
			return true, execErr
		}
	}
	return false, execErr
}

func execPostScript(script []string) error {
	var execErr error
	if len(script) > 0 {
		execErr = ExecScript(script...)
	}
	return execErr
}

func responseStatusMessage(
	inboxMsg *message.Message, envelope *protocol.Envelope, id string,
) ([]*message.Message, error) {
	if topic, ok := connector.TopicFromCtx(inboxMsg.Context()); ok {

		data, err := json.Marshal(envelope)
		if err == nil {
			outboxMsg := message.NewMessage(watermill.NewUUID(), []byte(data))
			outboxMsg.SetContext(connector.SetTopicToCtx(inboxMsg.Context(),
				util.ResponseStatusTopic(topic, envelope.Status)))
			return []*message.Message{outboxMsg}, nil

		}

		return nil, errors.Wrapf(err, "outbox response build error for correlation-id '%s'", id)
	}

	return nil, errors.New("missing response inbox command topic")
}

func (a *Agent) removePostBootstrapFile() {
	if len(a.settings.PostBootstrapFile) > 0 {
		if a.request == nil || a.request.response == nil || a.request.response.file == nil {
			return
		}
		a.request.response.file.Close()
		os.Remove(a.settings.PostBootstrapFile)
	}
}
