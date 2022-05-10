// Copyright (c) 2022 Contributors to the Eclipse Foundation
//
// See the NOTICE file(s) distributed with this work for additional
// information regarding copyright ownership.
//
// This program and the accompanying materials are made available under the
// terms of the Eclipse Public License 2.0 which is available at
// http://www.eclipse.org/legal/epl-2.0
//
// SPDX-License-Identifier: EPL-2.0

package bootstrapping_test

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"net/http"
	"os"
	"runtime"
	"testing"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/eclipse/ditto-clients-golang/model"
	"github.com/eclipse/ditto-clients-golang/protocol"
	"github.com/eclipse/ditto-clients-golang/protocol/things"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/eclipse-kanto/suite-connector/config"
	"github.com/eclipse-kanto/suite-connector/connector"
	"github.com/eclipse-kanto/suite-connector/logger"
	"github.com/eclipse-kanto/suite-connector/testutil"
	"github.com/eclipse-kanto/suite-connector/util"

	bs "github.com/eclipse-kanto/suite-bootstrapping/internal/bootstrapping"
)

const (
	testAttribute    = "topic.testPublisher"
	testProvisioning = `{"id": "org.eclipse.kanto:test"}`

	featureID = "Bootstrapping"

	requestTopic  = "org.eclipse.kanto/test/things/live/messages/request"
	responseTopic = "org.eclipse.kanto/test/things/live/messages/response"

	requestPath = "/features/" + featureID + "/outbox/messages/request"

	testDir = "testtemp"
)

type BootstrappingSuite struct {
	suite.Suite
	agent      *bs.Agent
	settings   *bs.BootstrapSettings
	publisher  message.Publisher
	lastStatus bool
	logger     logger.Logger
}

func TestCommonCommandsSuite(t *testing.T) {
	suite.Run(t, new(BootstrappingSuite))
}

func (s *BootstrappingSuite) S() *BootstrappingSuite {
	return s
}

func (s *BootstrappingSuite) SetupSuite() {
	s.logger = testutil.NewLogger("bootstrapping", logger.DEBUG)

	s.settings = &bs.BootstrapSettings{
		HubConnectionSettings: config.HubConnectionSettings{
			DeviceID: "org.eclipse.kanto:test",
		},
		PreBootstrapScript:        buildScriptArgs("echo", "preconfig"),
		PreBootstrapFile:          "agent_test.go",
		PostBootstrapScript:       buildScriptArgs("echo", "postconfig"),
		PostBootstrapFile:         testDir + "/test_postconfig",
		BootstrapProvisioningFile: testDir + "/test_provisioning.json",
		ChunkSize:                 2000,
	}

	agent, err := bs.NewAgent(s.settings, s.logger, s.HandleResponseResult)
	require.NoError(s.T(), err)
	s.agent = agent
	s.publisher = &testPublisher{
		buffer: list.New(),
	}
}

func buildScriptArgs(args ...string) []string {
	if runtime.GOOS == "windows" {
		args = append(args[:1], args[0:]...)
		args[0] = "cmd"
	}
	return args
}

func (s *BootstrappingSuite) HandleResponseResult(requestID string, succeeded bool) {
	s.lastStatus = succeeded
}

func (s *BootstrappingSuite) TearDownSuite() {
	if s.agent != nil {
		s.publisher.Close()
	}
}

func (s *BootstrappingSuite) TearDownTest() {
	s.lastStatus = false
	os.Remove(s.settings.PostBootstrapFile)
	os.Remove(s.settings.BootstrapProvisioningFile)
	os.Remove(testDir)

	s.publisher.(*testPublisher).buffer.Init()
}

type testPublisher struct {
	buffer *list.List
}

func (p *testPublisher) Publish(topic string, msgs ...*message.Message) error {
	for _, msg := range msgs {
		msg.Metadata.Set(testAttribute, topic)
		if p.buffer != nil {
			p.buffer.PushBack(msg)
		}
	}
	return nil
}

func (p *testPublisher) Close() error {
	return nil
}

func (p *testPublisher) Pull() (*message.Message, error) {
	if next := p.buffer.Front(); next != nil {
		return p.buffer.Remove(next).(*message.Message), nil
	}
	return nil, errors.New("no message published")
}

// Agent initialize tests

func (s *BootstrappingSuite) TestNewAgentWithNilSettings() {
	_, err := bs.NewAgent(nil, nil, nil)
	require.Error(s.T(), err)
}

func (s *BootstrappingSuite) TestNewAgentWithoutChunkSize() {
	invalidSettings := &bs.BootstrapSettings{
		HubConnectionSettings: config.HubConnectionSettings{
			DeviceID: "org.eclipse.kanto:test",
		},
	}

	_, err := bs.NewAgent(invalidSettings, nil, nil)
	require.Error(s.T(), err)
}

func (s *BootstrappingSuite) TestNewAgentWithEmptyDeviceID() {
	invalidSettings := &bs.BootstrapSettings{
		ChunkSize: 2000,
	}
	_, err := bs.NewAgent(invalidSettings, nil, nil)
	require.Error(s.T(), err)
}

// Create feature test

func (s *BootstrappingSuite) TestCreateBootstrappingFeature() {
	err := s.agent.CreateBootstrappingFeature(s.publisher)
	require.NoError(s.T(), err)
}

// Agent settings validation tests

func (s *BootstrappingSuite) TestSendRequestEmptyRequestID() {
	err := s.agent.PublishRequest("", s.publisher)
	require.Error(s.T(), err)
}

func (s *BootstrappingSuite) TestSendRequestNilPublisher() {
	err := s.agent.PublishRequest("NilPublisher", nil)
	require.Error(s.T(), err)
}

// Send request parameters validation tests

func (s *BootstrappingSuite) TestHandleResponseNoPostBootstrapFile() {
	requestID := "TestHandleResponseNoPostBootstrapFile"
	invalidSettings := &bs.BootstrapSettings{
		HubConnectionSettings: config.HubConnectionSettings{
			DeviceID: "org.eclipse.kanto:test",
		},
		ChunkSize: 2000,
	}

	agent, err := bs.NewAgent(invalidSettings, s.logger, s.HandleResponseResult)
	require.NoError(s.T(), err)

	// init - publish request
	err = agent.PublishRequest(requestID, s.publisher)
	require.NoError(s.T(), err)

	payload := &bs.ResponseData{
		ID: requestID,
	}

	hash := sha256.New()
	// fail on chunk handle and discard request id
	addChunk(payload, "chunk no PostBootstrapFile", hash)

	msgs, err := agent.HandleResponse(responseMsg(invalidSettings.DeviceID, payload, true))
	require.NoError(s.T(), err)
	s.assertErrorResponse(msgs, bs.NewMessageInternalError())

	// fail with unknown request id - no more chunks handling
	addChunk(payload, "chunk after handled error", hash)

	msgs, err = agent.HandleResponse(responseMsg(invalidSettings.DeviceID, payload, true))
	require.NoError(s.T(), err)
	s.assertErrorResponse(msgs, bs.NewMessageParameterInvalidError())

	addChunk(payload, "chunk after error with response-required false", hash)

	msgs, err = agent.HandleResponse(responseMsg(invalidSettings.DeviceID, payload, false))
	require.Error(s.T(), err)
	assert.Nil(s.T(), msgs)

	assert.False(s.S().T(), s.lastStatus)
}

// Send request tests

func (s *BootstrappingSuite) TestSendRequest() {
	requestID := "TestSendRequest"

	err := s.agent.PublishRequest(requestID, s.publisher)
	require.NoError(s.T(), err)

	require.True(s.T(), util.FileExists(s.settings.PreBootstrapFile))
	checkPublished(s, requestID, true, fileHash(s.T(), s.settings.PreBootstrapFile))
}

func (s *BootstrappingSuite) TestSendRequestNoPreconfig() {
	requestID := "TestSendRequestNoPreconfig"

	noPrescript := &bs.BootstrapSettings{
		HubConnectionSettings: config.HubConnectionSettings{
			DeviceID: "org.eclipse.kanto:test",
		},
		PreBootstrapFile: "nonexistig.test",
		ChunkSize:        1,
	}
	require.NoFileExists(s.T(), noPrescript.PreBootstrapFile)

	agent, err := bs.NewAgent(noPrescript, s.logger, nil)
	require.NoError(s.T(), err)

	err = agent.PublishRequest(requestID, s.publisher)
	require.NoError(s.T(), err)

	checkPublished(s, requestID, false, hex.EncodeToString(sha256.New().Sum(nil)))
}

func (s *BootstrappingSuite) TestSendRequestEmptyPreconfig() {
	requestID := "TestSendRequestEmptyPreconfig"

	emptyPrescript := &bs.BootstrapSettings{
		HubConnectionSettings: config.HubConnectionSettings{
			DeviceID: "org.eclipse.kanto:test",
		},
		PreBootstrapFile: "empty.test",
		ChunkSize:        1,
	}

	file, _ := os.Create(emptyPrescript.PreBootstrapFile)
	require.FileExists(s.T(), emptyPrescript.PreBootstrapFile)
	file.Close()
	defer os.Remove(emptyPrescript.PreBootstrapFile)

	agent, err := bs.NewAgent(emptyPrescript, s.logger, nil)
	require.NoError(s.T(), err)

	err = agent.PublishRequest(requestID, s.publisher)
	require.NoError(s.T(), err)

	checkPublished(s, requestID, false, hex.EncodeToString(sha256.New().Sum(nil)))
}

// Handle response tests

func (s *BootstrappingSuite) TestHandleResponse() {
	requestID := "TestHandleResponse"

	// publish request for the expected response
	err := s.agent.PublishRequest(requestID, s.publisher)
	require.NoError(s.T(), err)

	// publish several response messages with the same request ID
	payload := &bs.ResponseData{
		ID: requestID,
	}

	hash := sha256.New()
	s.assertNoProvCreatedOnNextChunk(payload, "chunk1", hash)
	s.assertNoProvCreatedOnNextChunk(payload, "chunk2", hash)

	payload.Chunk = ""
	payload.Provisioning = testProvisioning
	msgs, err := s.agent.HandleResponse(responseMsg(s.settings.DeviceID, payload, true))
	require.NoError(s.T(), err)

	s.assertOKResponse(msgs)

	assert.True(s.T(), util.FileExists(s.settings.PostBootstrapFile))
	assert.False(s.T(), util.FileExists(s.settings.BootstrapProvisioningFile))

	payload.Provisioning = ""
	addChunk(payload, "last", hash)
	addHashSum(payload, hash)
	_, err = s.agent.HandleResponse(responseMsg(s.settings.DeviceID, payload, true))
	require.NoError(s.T(), err)
	assert.True(s.T(), util.FileExists(s.settings.PostBootstrapFile), s.settings.PostBootstrapFile)
	assert.True(s.T(), util.FileExists(s.settings.BootstrapProvisioningFile), s.settings.BootstrapProvisioningFile)

	checkPublished(s, requestID, true, fileHash(s.T(), s.settings.PreBootstrapFile))
	assert.True(s.T(), s.lastStatus)
}

func (s *BootstrappingSuite) TestHandleResponseUnknownID() {
	requestID := "TestHandleResponseUnknownID"

	hash := sha256.New()

	payload := &bs.ResponseData{
		ID:           requestID,
		Provisioning: testProvisioning,
	}
	addChunk(payload, "last", hash)

	addHashSum(payload, hash)
	msgs, err := s.agent.HandleResponse(responseMsg(s.settings.DeviceID, payload, true))
	require.NoError(s.T(), err)

	s.assertErrorResponse(msgs, bs.NewMessageParameterInvalidError())

	assert.False(s.T(), util.FileExists(s.settings.PostBootstrapFile))
	assert.False(s.T(), util.FileExists(s.settings.BootstrapProvisioningFile))
	assert.False(s.S().T(), s.lastStatus)
}

func (s *BootstrappingSuite) TestHandleResponsePayloadError() {
	requestID := "TestHandleResponsePayloadError"

	err := s.agent.PublishRequest(requestID, s.publisher)
	require.NoError(s.T(), err)

	msgs, err := s.agent.HandleResponse(responseMsg(s.settings.DeviceID, "invalid payload", true))
	require.NoError(s.T(), err)
	s.assertErrorResponse(msgs, bs.NewMessageParameterInvalidError())

	assert.False(s.T(), util.FileExists(s.settings.PostBootstrapFile))
	assert.False(s.T(), util.FileExists(s.settings.BootstrapProvisioningFile))
	assert.False(s.S().T(), s.lastStatus)
}

func (s *BootstrappingSuite) TestHandleResponseEnvelopeError() {
	requestID := "TestHandleResponseEnvelopeError"

	err := s.agent.PublishRequest(requestID, s.publisher)
	require.NoError(s.T(), err)

	msg := message.NewMessage(watermill.NewUUID(), []byte("invalid envelope"))
	msg.SetContext(connector.SetTopicToCtx(context.TODO(), "command///req/test-id/response"))

	msgs, err := s.agent.HandleResponse(msg)
	require.Error(s.T(), err)
	assert.Nil(s.T(), msgs, msgs)

	assert.False(s.T(), util.FileExists(s.settings.PostBootstrapFile))
	assert.False(s.T(), util.FileExists(s.settings.BootstrapProvisioningFile))
	assert.False(s.S().T(), s.lastStatus)
}

func (s *BootstrappingSuite) TestHandleResponseNoBootstrapProvisioningFile() {
	requestID := "TestHandleResponseNoBootstrapProvisioningFile"

	invalidSettings := &bs.BootstrapSettings{
		HubConnectionSettings: config.HubConnectionSettings{
			DeviceID: "org.eclipse.kanto:test",
		},
		PostBootstrapFile: testDir + "/no_provisioning_postconfig",
		ChunkSize:         2000,
	}
	agent, err := bs.NewAgent(invalidSettings, s.logger, s.HandleResponseResult)
	require.NoError(s.T(), err)

	// init - publish request
	err = agent.PublishRequest(requestID, s.publisher)
	require.NoError(s.T(), err)

	payload := &bs.ResponseData{
		ID:           requestID,
		Provisioning: testProvisioning,
	}

	hash := sha256.New()
	addHashSum(payload, hash)
	msgs, err := agent.HandleResponse(responseMsg(invalidSettings.DeviceID, payload, true))
	require.NoError(s.T(), err)
	s.assertErrorResponse(msgs, bs.NewMessageInternalError())

	assert.False(s.T(), util.FileExists(invalidSettings.PostBootstrapFile))
	assert.False(s.S().T(), s.lastStatus)
}

func (s *BootstrappingSuite) TestHandleResponseNoPostBootstrapScript() {
	requestID := "TestHandleResponseNoPostBootstrapScript"
	invalidSettings := &bs.BootstrapSettings{
		HubConnectionSettings: config.HubConnectionSettings{
			DeviceID: "org.eclipse.kanto:test",
		},
		PostBootstrapFile: testDir + "/no_postconfig_script",
		ChunkSize:         2000,
	}
	agent, err := bs.NewAgent(invalidSettings, s.logger, nil)
	require.NoError(s.T(), err)

	defer os.Remove(invalidSettings.PostBootstrapFile)

	// init - publish request
	err = agent.PublishRequest(requestID, s.publisher)
	require.NoError(s.T(), err)

	payload := &bs.ResponseData{
		ID: requestID,
	}

	hash := sha256.New()
	addHashSum(payload, hash)
	msgs, err := agent.HandleResponse(responseMsg(invalidSettings.DeviceID, payload, true))
	require.NoError(s.T(), err)
	s.assertOKResponse(msgs)

	assert.True(s.T(), util.FileExists(invalidSettings.PostBootstrapFile))
	assert.False(s.S().T(), s.lastStatus)
}

func (s *BootstrappingSuite) TestHandleResponseUnexpectedHash() {
	requestID := "TestHandleResponseUnexpectedHash"

	// init - publish request
	err := s.agent.PublishRequest(requestID, s.publisher)
	require.NoError(s.T(), err)

	payload := &bs.ResponseData{
		ID:           requestID,
		Provisioning: testProvisioning,
	}

	hash := sha256.New()
	// fail on chunk handle and discard request id
	addChunk(payload, "chunk invalid check sum", hash)

	payload.Hash = "invalid check sum"
	msgs, err := s.agent.HandleResponse(responseMsg(s.settings.DeviceID, payload, true))
	require.NoError(s.T(), err)
	s.assertErrorResponse(msgs, bs.NewMessageParameterInvalidError())

	assert.False(s.T(), util.FileExists(s.settings.PostBootstrapFile))
	assert.False(s.S().T(), s.lastStatus)
}

func (s *BootstrappingSuite) TestHandleResponseErrorNoProvisioningPath() {
	requestID := "TestHandleResponseErrorNoProvisioningPath"

	invalidSettings := &bs.BootstrapSettings{
		HubConnectionSettings: config.HubConnectionSettings{
			DeviceID: "org.eclipse.kanto:test",
		},
		PostBootstrapFile: testDir + "/test_postconfig_no_prov_path",
		ChunkSize:         2000,
	}
	agent, err := bs.NewAgent(invalidSettings, s.logger, s.HandleResponseResult)
	require.NoError(s.T(), err)

	// init - publish request
	err = agent.PublishRequest(requestID, s.publisher)
	require.NoError(s.T(), err)

	payload := &bs.ResponseData{
		ID: requestID,
	}

	hash := sha256.New()
	// fail on chunk handle and discard request id
	addChunk(payload, "chunk", hash)

	// addHashSum(payload, hash)
	msgs, err := agent.HandleResponse(responseMsg(invalidSettings.DeviceID, payload, true))
	require.NoError(s.T(), err)
	assert.True(s.T(), util.FileExists(invalidSettings.PostBootstrapFile))
	s.assertOKResponse(msgs)

	payload.Provisioning = testProvisioning
	addChunk(payload, "next chunk", hash)

	addHashSum(payload, hash)
	msgs, err = agent.HandleResponse(responseMsg(invalidSettings.DeviceID, payload, true))
	require.NoError(s.T(), err)
	s.assertErrorResponse(msgs, bs.NewMessageInternalError())

	assert.False(s.T(), util.FileExists(invalidSettings.PostBootstrapFile))
	assert.False(s.S().T(), s.lastStatus)
}

func (s *BootstrappingSuite) TestHandleResponseError() {
	requestID := "TestHandleResponseError"

	// init - publish request
	err := s.agent.PublishRequest(requestID, s.publisher)
	require.NoError(s.T(), err)

	payload := &bs.ResponseData{
		ID: requestID,
	}

	hash := sha256.New()
	s.assertNoProvCreatedOnNextChunk(payload, "chunk", hash)

	payload.Err = 500
	addChunk(payload, "next chunk", hash)

	addHashSum(payload, hash)
	msgs, err := s.agent.HandleResponse(responseMsg(s.settings.DeviceID, payload, true))
	require.NoError(s.T(), err)
	s.assertOKResponse(msgs)

	assert.False(s.T(), util.FileExists(s.settings.PostBootstrapFile))
	assert.False(s.S().T(), s.lastStatus)
}

// Test utilities

func checkPublished(s *BootstrappingSuite, requestID string, hasChunk bool, hash string) {
	pub := s.publisher.(*testPublisher)

	len := pub.buffer.Len()
	assert.GreaterOrEqual(s.T(), len, 1)

	for i := 0; i < len; i++ {
		rsp, err := pub.Pull()
		require.NoError(s.T(), err, i)
		msg := &message.Message{
			Payload: []byte(rsp.Payload),
		}
		actual := protocol.Envelope{}
		err = json.Unmarshal(msg.Payload, &actual)
		require.NoError(s.T(), err, i)

		if i < len-1 {
			assertRequestEnvelope(s, requestID, hasChunk, "", i, &actual)
		} else {
			assertRequestEnvelope(s, requestID, hasChunk, hash, i, &actual)
		}
	}

	assert.Equal(s.T(), 0, pub.buffer.Len())
}

func assertRequestEnvelope(
	s *BootstrappingSuite, requestID string, hasChunk bool, hash string,
	index int, actual *protocol.Envelope,
) {
	assert.EqualValues(s.T(), requestTopic, actual.Topic.String(), index)
	assert.EqualValues(s.T(), requestPath, actual.Path, index)
	if actual.Value != nil {
		actualData := &bs.RequestData{}
		extractAsData(s.T(), actual.Value, actualData)

		require.Equal(s.T(), requestID, actualData.ID)
		assert.Equal(s.T(), hasChunk, len(actualData.Chunk) > 0, index)
		assert.Equal(s.T(), hash, actualData.Hash, index)
	}
}

func responseMsg(deviceID string, payload interface{}, required bool) *message.Message {
	msg := things.NewMessage(
		model.NewNamespacedIDFrom(deviceID)).
		Feature(featureID).
		Inbox("response").
		WithPayload(payload)

	testCorrelationID := ""
	if required {
		testCorrelationID = watermill.NewUUID()
	}
	env := msg.Envelope(protocol.WithCorrelationID(testCorrelationID),
		protocol.WithResponseRequired(required),
		protocol.WithContentType("application/json"))

	if data, err := json.Marshal(env); err == nil {
		msg := message.NewMessage(watermill.NewUUID(), []byte(data))
		msg.SetContext(connector.SetTopicToCtx(context.TODO(), "command///req/test-id/response"))
		return msg
	}
	return nil
}

func (s *BootstrappingSuite) assertNoProvCreatedOnNextChunk(
	payload *bs.ResponseData, chunk string, hash hash.Hash,
) {
	addChunk(payload, chunk, hash)

	_, err := s.agent.HandleResponse(responseMsg(s.settings.DeviceID, payload, false))
	require.NoError(s.T(), err)
	assert.True(s.T(), util.FileExists(s.settings.PostBootstrapFile), string(chunk))
	assert.False(s.T(), util.FileExists(s.settings.BootstrapProvisioningFile), string(chunk))
}

func addChunk(payload *bs.ResponseData, chunk string, hash hash.Hash) {
	payload.Chunk = base64.StdEncoding.EncodeToString([]byte(chunk))
	hash.Write([]byte(chunk))
}

func addHashSum(payload *bs.ResponseData, hash hash.Hash) {
	payload.Hash = hex.EncodeToString(hash.Sum(nil))
}

func (s *BootstrappingSuite) assertErrorResponse(msgs []*message.Message, expError *bs.MessageError) {
	require.Equal(s.T(), 1, len(msgs))
	env := s.assertMessageTopicStatus(msgs[0], expError.Status)

	msgError := &bs.MessageError{}
	extractAsData(s.T(), env.Value, msgError)

	assert.EqualValues(s.T(), expError, msgError)
}

func extractAsData(t *testing.T, value interface{}, data interface{}) {
	payload, err := json.Marshal(value)
	require.NoError(t, err)
	err = json.Unmarshal(payload, &data)
	require.NoError(t, err)
}

func (s *BootstrappingSuite) assertOKResponse(msgs []*message.Message) {
	require.Equal(s.T(), 1, len(msgs))
	env := s.assertMessageTopicStatus(msgs[0], http.StatusNoContent)

	assert.Nil(s.T(), env.Value)
}

func (s *BootstrappingSuite) assertMessageTopicStatus(msg *message.Message, status int) protocol.Envelope {
	expectedTopic := fmt.Sprintf("command///res/test-id/%v", status)

	actualTopic, ok := connector.TopicFromCtx(msg.Context())
	assert.True(s.T(), ok)
	assert.EqualValues(s.T(), expectedTopic, actualTopic)

	rspMsg := protocol.Envelope{}
	err := json.Unmarshal(msg.Payload, &rspMsg)
	require.NoError(s.T(), err)

	assert.EqualValues(s.T(), status, rspMsg.Status)
	return rspMsg
}
