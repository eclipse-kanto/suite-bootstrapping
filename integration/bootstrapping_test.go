// Copyright (c) 2023 Contributors to the Eclipse Foundation
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

//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/caarlos0/env/v6"
	"github.com/eclipse-kanto/kanto/integration/util"
	"github.com/eclipse/ditto-clients-golang"
	"github.com/eclipse/ditto-clients-golang/model"
	"github.com/eclipse/ditto-clients-golang/protocol"
	"github.com/eclipse/ditto-clients-golang/protocol/things"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	bootstrappingFeatureID             = "Bootstrapping"
	actionResponse                     = "response"
	msgFailedCreateWebsocketConnection = "failed to create websocket connection"
	eventFilterTemplate                = "like(resource:path,'/features/%s/*')"
	suiteBootstrappingService          = "suite-bootstrapping.service"
	suiteConnectorService              = "suite-connector.service"
)

// envTestCredentials holds credentials for test
type envTestCredentials struct {
	CaCert   string `env:"caCert" envDefault:"/etc/suite-connector/iothub.crt"`
	LogFile  string `env:"logFile" envDefault:"/var/log/suite-connector/suite-connector.log"`
	PolicyID string `env:"POLICY_ID"`
	ScConfig string `env:"configFile" envDefault:"/etc/suite-connector/config.json"`
	ScBackup string `env:"configFileBackup" envDefault:"/etc/suite-connector/configBackup.json"`
	BsConfig string `env:"configBootstrapFile" envDefault:"/etc/suite-bootstrapping/config.json"`

	DeviceRegistryAPIAddress string `env:"DEVICE_REGISTRY_API_ADDRESS"`

	DeviceRegistryAPIUsername string `env:"DEVICE_REGISTRY_API_USERNAME" envDefault:"ditto"`
	DeviceRegistryAPIPassword string `env:"DEVICE_REGISTRY_API_PASSWORD" envDefault:"ditto"`

	StatusTimeoutMs             int `env:"SCT_STATUS_TIMEOUT_MS" envDefault:"10000"`
	StatusReadySinceTimeDeltaMs int `env:"SCT_STATUS_READY_SINCE_TIME_DELTA_MS" envDefault:"0"`
	StatusRetryIntervalMs       int `env:"SCT_STATUS_RETRY_INTERVAL_MS" envDefault:"2000"`
}

type bootstrappingSuite struct {
	suite.Suite

	util.SuiteInitializer

	bootstrappingFeatureURL string

	envTestCredentials envTestCredentials

	requestID string
}

func (suite *bootstrappingSuite) SetupBootstrapping(t *testing.T) {
	cfg := &util.TestConfiguration{}

	opts := env.Options{RequiredIfNoDef: true}
	require.NoError(t, env.Parse(cfg, opts), "failed to process environment variables")

	t.Logf("%#v\n", cfg)

	mqttClient, err := util.NewMQTTClient(cfg)
	require.NoError(t, err, "connect to MQTT broker")

	dittoClient, err := ditto.NewClientMQTT(mqttClient, ditto.NewConfiguration())
	if err == nil {
		err = dittoClient.Connect()
	}

	if err != nil {
		mqttClient.Disconnect(uint(cfg.MQTTQuiesceMS))
		require.NoError(t, err, "initialize ditto client")
	}

	suite.Cfg = cfg
	suite.DittoClient = dittoClient
	suite.MQTTClient = mqttClient
	suite.ThingCfg, suite.requestID, err = getThingConfigurationBootstrapping(t, cfg)
	require.NoError(t, err, "cannot get thing configuration")
}

// getThingConfigurationBootstrapping retrieves information about the configured thing
func getThingConfigurationBootstrapping(t *testing.T, cfg *util.TestConfiguration) (*util.ThingConfiguration, string, error) {
	connMessages, err := util.NewDigitalTwinWSConnection(cfg)
	defer connMessages.Close()
	require.NoError(t, err, msgFailedCreateWebsocketConnection)

	err = util.SubscribeForWSMessages(cfg, connMessages, util.StartSendMessages,
		fmt.Sprintf(eventFilterTemplate, bootstrappingFeatureID))
	defer util.UnsubscribeFromWSMessages(cfg, connMessages, util.StopSendMessages)
	require.NoError(t, err, "error subscribing for WS messages")

	_, err = exec.Command("systemctl", "restart", suiteBootstrappingService).Output()
	require.NoError(t, err, "expected restart '%s' to be successful", suiteBootstrappingService)

	var thingConfig *util.ThingConfiguration
	var propertyRequestID string
	err = util.ProcessWSMessages(cfg, connMessages,
		func(msg *protocol.Envelope) (bool, error) {
			if msg.Path == util.GetFeatureOutboxMessagePath(bootstrappingFeatureID, "request") {
				thingConfig = &util.ThingConfiguration{
					DeviceID: fmt.Sprintf("%s:%s", msg.Topic.Namespace, msg.Topic.EntityName),
				}
				value, err := parseMap(msg.Value)
				if err != nil {
					return true, err
				}

				propertyRequestID, err = parseString(value["requestId"])
				if err != nil {
					return true, err
				}
				return true, nil

			}
			return true, fmt.Errorf("unexpected value: %v", msg.Value)
		})

	return thingConfig, propertyRequestID, err
}

func (suite *bootstrappingSuite) SetupSuite() {
	suite.SetupBootstrapping(suite.T())

	bootstrappingThingURL := util.GetThingURL(suite.Cfg.DigitalTwinAPIAddress, suite.ThingCfg.DeviceID)
	suite.bootstrappingFeatureURL = util.GetFeatureURL(bootstrappingThingURL, bootstrappingFeatureID)

	envTestCredentials, err := getEnvironmentTestCredentials()
	require.NoError(suite.T(), err, "error getting environment credentials")
	suite.envTestCredentials = envTestCredentials
}

func getEnvironmentTestCredentials() (envTestCredentials, error) {
	opts := env.Options{RequiredIfNoDef: true}
	creds := envTestCredentials{}
	err := env.Parse(&creds, opts)
	return creds, err
}

func (suite *bootstrappingSuite) TearDownSuite() {
	suite.SuiteInitializer.TearDown()
}

func TestBootstrappingSuite(t *testing.T) {
	suite.Run(t, new(bootstrappingSuite))
}

func (suite *bootstrappingSuite) TestBootstrapping() {
	bootCfg, err := getBootstrapConfigStruct(suite.envTestCredentials.BsConfig)
	require.NoError(suite.T(), err, "error getting bootstrapping configuration")

	deviceID := bootCfg.DeviceID + "FromBootstrapping"

	cfg := &util.ConnectorConfiguration{
		CaCert:   suite.envTestCredentials.CaCert,
		LogFile:  suite.envTestCredentials.LogFile,
		Address:  bootCfg.Address,
		DeviceID: deviceID,
		TenantID: bootCfg.TenantID,
		AuthID:   strings.ReplaceAll(deviceID, ":", "_"),
		Password: bootCfg.Password,
	}

	registryAPI := strings.TrimSuffix(suite.envTestCredentials.DeviceRegistryAPIAddress, "/") + "/v1"
	createdResources := util.CreateDeviceResources(
		deviceID, cfg.TenantID, suite.envTestCredentials.PolicyID, cfg.Password, registryAPI,
		suite.envTestCredentials.DeviceRegistryAPIUsername, suite.envTestCredentials.DeviceRegistryAPIPassword, suite.Cfg)

	url := getTenantURL(suite.envTestCredentials.DeviceRegistryAPIAddress, cfg.TenantID)

	err = util.RegisterDeviceResources(suite.Cfg, createdResources, deviceID, url,
		suite.envTestCredentials.DeviceRegistryAPIUsername, suite.envTestCredentials.DeviceRegistryAPIPassword)
	defer util.DeleteResources(suite.Cfg, createdResources, deviceID, url,
		suite.envTestCredentials.DeviceRegistryAPIUsername, suite.envTestCredentials.DeviceRegistryAPIPassword)
	require.NoError(suite.T(), err, "error registering devices")

	src := suite.envTestCredentials.ScConfig
	dst := suite.envTestCredentials.ScBackup
	err = backupRestoreFile(src, dst, false)
	require.NoError(suite.T(), err, "unable to backup suite-connector service config file '%s'", src)

	wsConnection, err := util.NewDigitalTwinWSConnection(suite.Cfg)
	defer wsConnection.Close()
	require.NoError(suite.T(), err, msgFailedCreateWebsocketConnection)

	err = util.SubscribeForWSMessages(suite.Cfg, wsConnection, util.StartSendMessages,
		fmt.Sprintf(eventFilterTemplate, bootstrappingFeatureID))
	defer util.UnsubscribeFromWSMessages(suite.Cfg, wsConnection, util.StopSendMessages)
	require.NoError(suite.T(), err, "unable to listen for messages by using a websocket connection")

	payload, err := createBootstrappingResponsePayload(cfg, suite.requestID)
	require.NoError(suite.T(), err, "unable to create payload of bootstrapping response")

	_, err = util.ExecuteOperation(suite.Cfg, suite.bootstrappingFeatureURL, actionResponse, payload)
	require.NoError(suite.T(), err, "failed to send response to bootstrapping")
	result := util.ProcessWSMessages(suite.Cfg, wsConnection, func(msg *protocol.Envelope) (bool, error) {
		if msg.Path != util.GetFeatureInboxMessagePath(bootstrappingFeatureID, actionResponse) {
			return false, nil
		}

		if msg.Topic.String() != util.GetLiveMessageTopic(suite.ThingCfg.DeviceID, protocol.TopicAction(actionResponse)) {
			return false, nil
		}

		value, err := parseMap(msg.Value)
		if err != nil {
			return true, err
		}

		propertyRequestID, err := parseString(value["requestId"])
		if err != nil {
			return true, err
		}
		return propertyRequestID == suite.requestID, nil
	})

	require.NoError(suite.T(), result, "error while receiving suite-bootstrapping response")

	suite.isConnected(deviceID)

	cmd := things.NewCommand(model.NewNamespacedIDFrom(deviceID)).Twin().Feature("ConnectorTestFeature").
		Modify((&model.Feature{}).WithProperty("testProperty", "testValue"))
	msg := cmd.Envelope(protocol.WithResponseRequired(true))

	err = suite.DittoClient.Send(msg)
	require.NoError(suite.T(), err, "create test feature")

	_, err = exec.Command("systemctl", "stop", suiteConnectorService).Output()
	require.NoError(suite.T(), err, "unable to stop '%s'", suiteConnectorService)

	err = backupRestoreFile(dst, src, true)
	require.NoError(suite.T(), err, "unable to restore suite-connector service config file '%s'", dst)
}

func (suite *bootstrappingSuite) isConnected(deviceID string) {
	type connectionStatus struct {
		ReadySince time.Time `json:"readySince"`
		ReadyUntil time.Time `json:"readyUntil"`
	}

	timeout := util.MillisToDuration(suite.envTestCredentials.StatusTimeoutMs)
	threshold := time.Now().Add(timeout)

	firstTime := true
	sleepDuration := util.MillisToDuration(suite.envTestCredentials.StatusRetryIntervalMs)
	for {
		if !firstTime {
			time.Sleep(sleepDuration)
		}
		firstTime = false

		body, err := util.GetFeaturePropertyValue(suite.Cfg,
			util.GetFeatureURL(util.GetThingURL(suite.Cfg.DigitalTwinAPIAddress, deviceID), "ConnectionStatus"), "status")
		now := time.Now()
		if err != nil {
			if now.Before(threshold) {
				continue
			}
			suite.T().Errorf("connection status property not available: %v", err)
			break
		}

		status := &connectionStatus{}
		err = json.Unmarshal(body, status)
		require.NoError(suite.T(), err, "connection status should be parsed")

		forever := time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC)
		if !forever.Equal(status.ReadyUntil) {
			if now.Before(threshold) {
				continue
			}
			suite.T().Errorf("readyUntil should be %v", forever)
			break
		}

		delta := int64(suite.envTestCredentials.StatusReadySinceTimeDeltaMs)

		require.Lessf(suite.T(), status.ReadySince.UnixMilli(),
			time.Now().UnixMilli()+delta, "readySince should be before current time")
		break
	}
}
