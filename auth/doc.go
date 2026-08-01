// Package auth implements the credential-acquisition flows an application
// wires its own OAuth endpoints into: an authorization-code loopback server
// (LoopbackAuthCode), the device-authorization grant (DeviceToken), and a
// small helper for shelling out to auth CLIs (RunTool).
//
// The package only obtains credentials; deciding what those credentials are
// authorized to do stays with the application (see the mode package's Gate).
package auth
