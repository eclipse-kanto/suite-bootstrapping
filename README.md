![Kanto logo](https://github.com/eclipse-kanto/kanto/raw/main/logo/kanto.svg)

# Eclipse Kanto - Suite Bootstrapping

The suite bootstrapping provides a mechanism for automatic provisioning of devices for connecting to a target suite.
Devices can be shipped with a uniform clean setup without knowing the target suite subscription or tenancy
information, and without knowing the target device credentials.

Suite Bootstrapping implements the [Suite Bootstrapping](https://github.com/eclipse/vorto/tree/development/models/com.bosch.iot.suite-Bootstrapping-1.0.0.fbmodel) Vorto model.

The bootstrapping mechanism works as follows:
*Enrollment – All devices must be introduced individually before bootstrapping. Without enrollment, devices are not
accepted for bootstrapping.
*Bootstrap request – makes a request to the bootstrapping subscription, as opposed to the real subscription.
*Bootstrap response – a custom application receives the request and provisions the subscription. Then returns
provisioning.json - the configuration that is necessary for connecting with the field subscription, plus any
additional custom info.
*The "provisioning.json" is stored on the device and the connection switches to the target suite service instance.
*Device is now connecting to the target subscription.

## Community

* [GitHub Issues](https://github.com/eclipse-kanto/suite-bootstrapping/issues)
* [Mailing List](https://accounts.eclipse.org/mailing-list/kanto-dev)
