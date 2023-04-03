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
	sbServiceName                      = "suite-bootstrapping.service"
	suiteConnector                     = "suite-connector.service"
)

// EnvironmentTestCredentials holds credentials for test
type environmentTestCredentials struct {
	CaCert   string `env:"caCert" envDefault:"/etc/suite-connector/iothub.crt"`
	LogFile  string `env:"logFile" envDefault:"/var/log/suite-connector/suite-connector.log"`
	PolicyId string `env:"POLICY_ID"`
	ScConfig string `env:"configFile" envDefault:"/etc/suite-connector/config.json"`
	ScBackup string `env:"configFileBackup" envDefault:"/etc/suite-connector/configBackup.json"`
	BsConfig string `env:"configBootstrapFile" envDefault:"/etc/suite-bootstrapping/config.json"`

	DeviceRegistryAPIAddress string `env:"DEVICE_REGISTRY_API_ADDRESS"`

	DeviceRegistryAPIUsername string `env:"DEVICE_REGISTRY_API_USERNAME" envDefault:"ditto"`
	DeviceRegistryAPIPassword string `env:"DEVICE_REGISTRY_API_PASSWORD" envDefault:"ditto"`
}

type bootstrappingSuite struct {
	suite.Suite

	util.SuiteInitializer

	bootstrappingThingURL   string
	bootstrappingFeatureURL string

	envCredentials environmentTestCredentials

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
	if err != nil {
		defer suite.TearDown()
		require.NoError(t, err, "cannot get thing configuration")
	}
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

	_, err = exec.Command("systemctl", "restart", sbServiceName).Output()
	require.NoError(t, err, "expected restart operation to be successful")

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

	suite.bootstrappingThingURL = util.GetThingURL(suite.Cfg.DigitalTwinAPIAddress, suite.ThingCfg.DeviceID)
	suite.bootstrappingFeatureURL = util.GetFeatureURL(suite.bootstrappingThingURL, bootstrappingFeatureID)

	envCredentials, err := getEnvironmentTestCredentials()
	require.NoError(suite.T(), err, "error getting environment credentials")
	suite.envCredentials = envCredentials
}

func getEnvironmentTestCredentials() (environmentTestCredentials, error) {
	opts := env.Options{RequiredIfNoDef: true}
	creds := environmentTestCredentials{}
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
	bootCfg, err := getBootstrapConfigStruct(suite.envCredentials.BsConfig)
	require.NoError(suite.T(), err, "error getting bootstrapping configuration")

	deviceId := bootCfg.DeviceID + "FromBootstrapping"

	cfg := &util.ConnectorConfiguration{
		CaCert:   suite.envCredentials.CaCert,
		LogFile:  suite.envCredentials.LogFile,
		Address:  bootCfg.Address,
		DeviceID: deviceId,
		TenantID: bootCfg.TenantID,
		AuthID:   strings.ReplaceAll(deviceId, ":", "_"),
		Password: bootCfg.Password,
	}

	registryAPI := strings.TrimSuffix(suite.envCredentials.DeviceRegistryAPIAddress, "/") + "/v1"
	createdResources := util.CreateDeviceResources(
		deviceId, cfg.TenantID, suite.envCredentials.PolicyId, cfg.Password, registryAPI,
		suite.envCredentials.DeviceRegistryAPIUsername, suite.envCredentials.DeviceRegistryAPIPassword, suite.Cfg)

	url := getTenantURL(suite.envCredentials.DeviceRegistryAPIAddress, cfg.TenantID)

	err = util.RegisterDeviceResources(suite.Cfg, createdResources, deviceId, url,
		suite.envCredentials.DeviceRegistryAPIUsername, suite.envCredentials.DeviceRegistryAPIPassword)
	defer util.DeleteResources(suite.Cfg, createdResources, deviceId, url,
		suite.envCredentials.DeviceRegistryAPIUsername, suite.envCredentials.DeviceRegistryAPIPassword)
	require.NoError(suite.T(), err, "error registering devices")

	src := suite.envCredentials.ScConfig
	dst := suite.envCredentials.ScBackup
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

	time.Sleep(20 * time.Second)

	cmd := things.NewCommand(model.NewNamespacedIDFrom(deviceId)).Twin().Feature("ConnectorTestFeature").
		Modify((&model.Feature{}).WithProperty("testProperty", "testValue"))
	msg := cmd.Envelope(protocol.WithResponseRequired(true))

	err = suite.DittoClient.Send(msg)
	require.NoError(suite.T(), err, "create test feature")

	_, err = exec.Command("systemctl", "stop", suiteConnector).Output()
	require.NoError(suite.T(), err, "unable to stop '%s'", suiteConnector)

	err = backupRestoreFile(dst, src, true)
	require.NoError(suite.T(), err, "unable to restore suite-connector service config file '%s'", dst)
}
