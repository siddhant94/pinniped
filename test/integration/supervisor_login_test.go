// Copyright 2020-2021 the Pinniped contributors. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	coreosoidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1alpha1 "go.pinniped.dev/generated/latest/apis/supervisor/config/v1alpha1"
	idpv1alpha1 "go.pinniped.dev/generated/latest/apis/supervisor/idp/v1alpha1"
	"go.pinniped.dev/internal/certauthority"
	"go.pinniped.dev/internal/oidc"
	"go.pinniped.dev/internal/testutil"
	"go.pinniped.dev/pkg/oidcclient/nonce"
	"go.pinniped.dev/pkg/oidcclient/pkce"
	"go.pinniped.dev/pkg/oidcclient/state"
	"go.pinniped.dev/test/testlib"
	"go.pinniped.dev/test/testlib/browsertest"
)

func TestSupervisorLogin(t *testing.T) {
	env := testlib.IntegrationEnv(t)

	tests := []struct {
		name                                 string
		maybeSkip                            func(t *testing.T)
		createIDP                            func(t *testing.T)
		requestAuthorization                 func(t *testing.T, downstreamAuthorizeURL, downstreamCallbackURL string, httpClient *http.Client)
		wantDownstreamIDTokenSubjectToMatch  string
		wantDownstreamIDTokenUsernameToMatch string
		wantDownstreamIDTokenGroups          []string
		wantErrorDescription                 string
		wantErrorType                        string
	}{
		{
			name: "oidc with default username and groups claim settings",
			maybeSkip: func(t *testing.T) {
				// never need to skip this test
			},
			createIDP: func(t *testing.T) {
				t.Helper()
				testlib.CreateTestOIDCIdentityProvider(t, idpv1alpha1.OIDCIdentityProviderSpec{
					Issuer: env.SupervisorUpstreamOIDC.Issuer,
					TLS: &idpv1alpha1.TLSSpec{
						CertificateAuthorityData: base64.StdEncoding.EncodeToString([]byte(env.SupervisorUpstreamOIDC.CABundle)),
					},
					Client: idpv1alpha1.OIDCClient{
						SecretName: testlib.CreateClientCredsSecret(t, env.SupervisorUpstreamOIDC.ClientID, env.SupervisorUpstreamOIDC.ClientSecret).Name,
					},
				}, idpv1alpha1.PhaseReady)
			},
			requestAuthorization: requestAuthorizationUsingBrowserAuthcodeFlow,
			// the ID token Subject should include the upstream user ID after the upstream issuer name
			wantDownstreamIDTokenSubjectToMatch: "^" + regexp.QuoteMeta(env.SupervisorUpstreamOIDC.Issuer+"?sub=") + ".+",
			// the ID token Username should include the upstream user ID after the upstream issuer name
			wantDownstreamIDTokenUsernameToMatch: "^" + regexp.QuoteMeta(env.SupervisorUpstreamOIDC.Issuer+"?sub=") + ".+",
		},
		{
			name: "oidc with custom username and groups claim settings",
			maybeSkip: func(t *testing.T) {
				// never need to skip this test
			},
			createIDP: func(t *testing.T) {
				t.Helper()
				testlib.CreateTestOIDCIdentityProvider(t, idpv1alpha1.OIDCIdentityProviderSpec{
					Issuer: env.SupervisorUpstreamOIDC.Issuer,
					TLS: &idpv1alpha1.TLSSpec{
						CertificateAuthorityData: base64.StdEncoding.EncodeToString([]byte(env.SupervisorUpstreamOIDC.CABundle)),
					},
					Client: idpv1alpha1.OIDCClient{
						SecretName: testlib.CreateClientCredsSecret(t, env.SupervisorUpstreamOIDC.ClientID, env.SupervisorUpstreamOIDC.ClientSecret).Name,
					},
					Claims: idpv1alpha1.OIDCClaims{
						Username: env.SupervisorUpstreamOIDC.UsernameClaim,
						Groups:   env.SupervisorUpstreamOIDC.GroupsClaim,
					},
					AuthorizationConfig: idpv1alpha1.OIDCAuthorizationConfig{
						AdditionalScopes: env.SupervisorUpstreamOIDC.AdditionalScopes,
					},
				}, idpv1alpha1.PhaseReady)
			},
			requestAuthorization:                 requestAuthorizationUsingBrowserAuthcodeFlow,
			wantDownstreamIDTokenSubjectToMatch:  "^" + regexp.QuoteMeta(env.SupervisorUpstreamOIDC.Issuer+"?sub=") + ".+",
			wantDownstreamIDTokenUsernameToMatch: "^" + regexp.QuoteMeta(env.SupervisorUpstreamOIDC.Username) + "$",
			wantDownstreamIDTokenGroups:          env.SupervisorUpstreamOIDC.ExpectedGroups,
		},
		{
			name: "oidc with CLI password flow",
			maybeSkip: func(t *testing.T) {
				// never need to skip this test
			},
			createIDP: func(t *testing.T) {
				t.Helper()
				testlib.CreateTestOIDCIdentityProvider(t, idpv1alpha1.OIDCIdentityProviderSpec{
					Issuer: env.SupervisorUpstreamOIDC.Issuer,
					TLS: &idpv1alpha1.TLSSpec{
						CertificateAuthorityData: base64.StdEncoding.EncodeToString([]byte(env.SupervisorUpstreamOIDC.CABundle)),
					},
					Client: idpv1alpha1.OIDCClient{
						SecretName: testlib.CreateClientCredsSecret(t, env.SupervisorUpstreamOIDC.ClientID, env.SupervisorUpstreamOIDC.ClientSecret).Name,
					},
					AuthorizationConfig: idpv1alpha1.OIDCAuthorizationConfig{
						AllowPasswordGrant: true, // allow the CLI password flow for this OIDCIdentityProvider
					},
				}, idpv1alpha1.PhaseReady)
			},
			requestAuthorization: func(t *testing.T, downstreamAuthorizeURL, _ string, httpClient *http.Client) {
				requestAuthorizationUsingCLIPasswordFlow(t,
					downstreamAuthorizeURL,
					env.SupervisorUpstreamOIDC.Username, // username to present to server during login
					env.SupervisorUpstreamOIDC.Password, // password to present to server during login
					httpClient,
					false,
				)
			},
			// the ID token Subject should include the upstream user ID after the upstream issuer name
			wantDownstreamIDTokenSubjectToMatch: "^" + regexp.QuoteMeta(env.SupervisorUpstreamOIDC.Issuer+"?sub=") + ".+",
			// the ID token Username should include the upstream user ID after the upstream issuer name
			wantDownstreamIDTokenUsernameToMatch: "^" + regexp.QuoteMeta(env.SupervisorUpstreamOIDC.Issuer+"?sub=") + ".+",
		},
		{
			name: "ldap with email as username and groups names as DNs and using an LDAP provider which supports TLS",
			maybeSkip: func(t *testing.T) {
				t.Helper()
				if len(env.ToolsNamespace) == 0 && !env.HasCapability(testlib.CanReachInternetLDAPPorts) {
					t.Skip("LDAP integration test requires connectivity to an LDAP server")
				}
			},
			createIDP: func(t *testing.T) {
				t.Helper()
				secret := testlib.CreateTestSecret(t, env.SupervisorNamespace, "ldap-service-account", v1.SecretTypeBasicAuth,
					map[string]string{
						v1.BasicAuthUsernameKey: env.SupervisorUpstreamLDAP.BindUsername,
						v1.BasicAuthPasswordKey: env.SupervisorUpstreamLDAP.BindPassword,
					},
				)
				ldapIDP := testlib.CreateTestLDAPIdentityProvider(t, idpv1alpha1.LDAPIdentityProviderSpec{
					Host: env.SupervisorUpstreamLDAP.Host,
					TLS: &idpv1alpha1.TLSSpec{
						CertificateAuthorityData: base64.StdEncoding.EncodeToString([]byte(env.SupervisorUpstreamLDAP.CABundle)),
					},
					Bind: idpv1alpha1.LDAPIdentityProviderBind{
						SecretName: secret.Name,
					},
					UserSearch: idpv1alpha1.LDAPIdentityProviderUserSearch{
						Base:   env.SupervisorUpstreamLDAP.UserSearchBase,
						Filter: "",
						Attributes: idpv1alpha1.LDAPIdentityProviderUserSearchAttributes{
							Username: env.SupervisorUpstreamLDAP.TestUserMailAttributeName,
							UID:      env.SupervisorUpstreamLDAP.TestUserUniqueIDAttributeName,
						},
					},
					GroupSearch: idpv1alpha1.LDAPIdentityProviderGroupSearch{
						Base:   env.SupervisorUpstreamLDAP.GroupSearchBase,
						Filter: "",
						Attributes: idpv1alpha1.LDAPIdentityProviderGroupSearchAttributes{
							GroupName: "dn",
						},
					},
				}, idpv1alpha1.LDAPPhaseReady)
				expectedMsg := fmt.Sprintf(
					`successfully able to connect to "%s" and bind as user "%s" [validated with Secret "%s" at version "%s"]`,
					env.SupervisorUpstreamLDAP.Host, env.SupervisorUpstreamLDAP.BindUsername,
					secret.Name, secret.ResourceVersion,
				)
				requireSuccessfulLDAPIdentityProviderConditions(t, ldapIDP, expectedMsg)
			},
			requestAuthorization: func(t *testing.T, downstreamAuthorizeURL, _ string, httpClient *http.Client) {
				requestAuthorizationUsingCLIPasswordFlow(t,
					downstreamAuthorizeURL,
					env.SupervisorUpstreamLDAP.TestUserMailAttributeValue, // username to present to server during login
					env.SupervisorUpstreamLDAP.TestUserPassword,           // password to present to server during login
					httpClient,
					false,
				)
			},
			// the ID token Subject should be the Host URL plus the value pulled from the requested UserSearch.Attributes.UID attribute
			wantDownstreamIDTokenSubjectToMatch: "^" + regexp.QuoteMeta(
				"ldaps://"+env.SupervisorUpstreamLDAP.Host+
					"?base="+url.QueryEscape(env.SupervisorUpstreamLDAP.UserSearchBase)+
					"&sub="+base64.RawURLEncoding.EncodeToString([]byte(env.SupervisorUpstreamLDAP.TestUserUniqueIDAttributeValue)),
			) + "$",
			// the ID token Username should have been pulled from the requested UserSearch.Attributes.Username attribute
			wantDownstreamIDTokenUsernameToMatch: "^" + regexp.QuoteMeta(env.SupervisorUpstreamLDAP.TestUserMailAttributeValue) + "$",
			wantDownstreamIDTokenGroups:          env.SupervisorUpstreamLDAP.TestUserDirectGroupsDNs,
		},
		{
			name: "ldap with CN as username and group names as CNs and using an LDAP provider which only supports StartTLS", // try another variation of configuration options
			maybeSkip: func(t *testing.T) {
				t.Helper()
				if len(env.ToolsNamespace) == 0 && !env.HasCapability(testlib.CanReachInternetLDAPPorts) {
					t.Skip("LDAP integration test requires connectivity to an LDAP server")
				}
			},
			createIDP: func(t *testing.T) {
				t.Helper()
				secret := testlib.CreateTestSecret(t, env.SupervisorNamespace, "ldap-service-account", v1.SecretTypeBasicAuth,
					map[string]string{
						v1.BasicAuthUsernameKey: env.SupervisorUpstreamLDAP.BindUsername,
						v1.BasicAuthPasswordKey: env.SupervisorUpstreamLDAP.BindPassword,
					},
				)
				ldapIDP := testlib.CreateTestLDAPIdentityProvider(t, idpv1alpha1.LDAPIdentityProviderSpec{
					Host: env.SupervisorUpstreamLDAP.StartTLSOnlyHost,
					TLS: &idpv1alpha1.TLSSpec{
						CertificateAuthorityData: base64.StdEncoding.EncodeToString([]byte(env.SupervisorUpstreamLDAP.CABundle)),
					},
					Bind: idpv1alpha1.LDAPIdentityProviderBind{
						SecretName: secret.Name,
					},
					UserSearch: idpv1alpha1.LDAPIdentityProviderUserSearch{
						Base:   env.SupervisorUpstreamLDAP.UserSearchBase,
						Filter: "cn={}", // try using a non-default search filter
						Attributes: idpv1alpha1.LDAPIdentityProviderUserSearchAttributes{
							Username: "dn", // try using the user's DN as the downstream username
							UID:      env.SupervisorUpstreamLDAP.TestUserUniqueIDAttributeName,
						},
					},
					GroupSearch: idpv1alpha1.LDAPIdentityProviderGroupSearch{
						Base:   env.SupervisorUpstreamLDAP.GroupSearchBase,
						Filter: "",
						Attributes: idpv1alpha1.LDAPIdentityProviderGroupSearchAttributes{
							GroupName: "cn",
						},
					},
				}, idpv1alpha1.LDAPPhaseReady)
				expectedMsg := fmt.Sprintf(
					`successfully able to connect to "%s" and bind as user "%s" [validated with Secret "%s" at version "%s"]`,
					env.SupervisorUpstreamLDAP.StartTLSOnlyHost, env.SupervisorUpstreamLDAP.BindUsername,
					secret.Name, secret.ResourceVersion,
				)
				requireSuccessfulLDAPIdentityProviderConditions(t, ldapIDP, expectedMsg)
			},
			requestAuthorization: func(t *testing.T, downstreamAuthorizeURL, _ string, httpClient *http.Client) {
				requestAuthorizationUsingCLIPasswordFlow(t,
					downstreamAuthorizeURL,
					env.SupervisorUpstreamLDAP.TestUserCN,       // username to present to server during login
					env.SupervisorUpstreamLDAP.TestUserPassword, // password to present to server during login
					httpClient,
					false,
				)
			},
			// the ID token Subject should be the Host URL plus the value pulled from the requested UserSearch.Attributes.UID attribute
			wantDownstreamIDTokenSubjectToMatch: "^" + regexp.QuoteMeta(
				"ldaps://"+env.SupervisorUpstreamLDAP.StartTLSOnlyHost+
					"?base="+url.QueryEscape(env.SupervisorUpstreamLDAP.UserSearchBase)+
					"&sub="+base64.RawURLEncoding.EncodeToString([]byte(env.SupervisorUpstreamLDAP.TestUserUniqueIDAttributeValue)),
			) + "$",
			// the ID token Username should have been pulled from the requested UserSearch.Attributes.Username attribute
			wantDownstreamIDTokenUsernameToMatch: "^" + regexp.QuoteMeta(env.SupervisorUpstreamLDAP.TestUserDN) + "$",
			wantDownstreamIDTokenGroups:          env.SupervisorUpstreamLDAP.TestUserDirectGroupsCNs,
		},
		{
			name: "logging in to ldap with the wrong password fails",
			maybeSkip: func(t *testing.T) {
				t.Helper()
				if len(env.ToolsNamespace) == 0 && !env.HasCapability(testlib.CanReachInternetLDAPPorts) {
					t.Skip("LDAP integration test requires connectivity to an LDAP server")
				}
			},
			createIDP: func(t *testing.T) {
				t.Helper()
				secret := testlib.CreateTestSecret(t, env.SupervisorNamespace, "ldap-service-account", v1.SecretTypeBasicAuth,
					map[string]string{
						v1.BasicAuthUsernameKey: env.SupervisorUpstreamLDAP.BindUsername,
						v1.BasicAuthPasswordKey: env.SupervisorUpstreamLDAP.BindPassword,
					},
				)
				ldapIDP := testlib.CreateTestLDAPIdentityProvider(t, idpv1alpha1.LDAPIdentityProviderSpec{
					Host: env.SupervisorUpstreamLDAP.Host,
					TLS: &idpv1alpha1.TLSSpec{
						CertificateAuthorityData: base64.StdEncoding.EncodeToString([]byte(env.SupervisorUpstreamLDAP.CABundle)),
					},
					Bind: idpv1alpha1.LDAPIdentityProviderBind{
						SecretName: secret.Name,
					},
					UserSearch: idpv1alpha1.LDAPIdentityProviderUserSearch{
						Base:   env.SupervisorUpstreamLDAP.UserSearchBase,
						Filter: "",
						Attributes: idpv1alpha1.LDAPIdentityProviderUserSearchAttributes{
							Username: env.SupervisorUpstreamLDAP.TestUserMailAttributeName,
							UID:      env.SupervisorUpstreamLDAP.TestUserUniqueIDAttributeName,
						},
					},
					GroupSearch: idpv1alpha1.LDAPIdentityProviderGroupSearch{
						Base:   env.SupervisorUpstreamLDAP.GroupSearchBase,
						Filter: "",
						Attributes: idpv1alpha1.LDAPIdentityProviderGroupSearchAttributes{
							GroupName: "dn",
						},
					},
				}, idpv1alpha1.LDAPPhaseReady)
				expectedMsg := fmt.Sprintf(
					`successfully able to connect to "%s" and bind as user "%s" [validated with Secret "%s" at version "%s"]`,
					env.SupervisorUpstreamLDAP.Host, env.SupervisorUpstreamLDAP.BindUsername,
					secret.Name, secret.ResourceVersion,
				)
				requireSuccessfulLDAPIdentityProviderConditions(t, ldapIDP, expectedMsg)
			},
			requestAuthorization: func(t *testing.T, downstreamAuthorizeURL, _ string, httpClient *http.Client) {
				requestAuthorizationUsingCLIPasswordFlow(t,
					downstreamAuthorizeURL,
					env.SupervisorUpstreamLDAP.TestUserMailAttributeValue, // username to present to server during login
					"incorrect", // password to present to server during login
					httpClient,
					true,
				)
			},
			wantErrorDescription: "The resource owner or authorization server denied the request. Username/password not accepted by LDAP provider.",
			wantErrorType:        "access_denied",
		},
		{
			name: "ldap login still works after updating bind secret",
			maybeSkip: func(t *testing.T) {
				t.Helper()
				if len(env.ToolsNamespace) == 0 && !env.HasCapability(testlib.CanReachInternetLDAPPorts) {
					t.Skip("LDAP integration test requires connectivity to an LDAP server")
				}
			},
			createIDP: func(t *testing.T) {
				t.Helper()

				secret := testlib.CreateTestSecret(t, env.SupervisorNamespace, "ldap-service-account", v1.SecretTypeBasicAuth,
					map[string]string{
						v1.BasicAuthUsernameKey: env.SupervisorUpstreamLDAP.BindUsername,
						v1.BasicAuthPasswordKey: env.SupervisorUpstreamLDAP.BindPassword,
					},
				)
				secretName := secret.Name
				ldapIDP := testlib.CreateTestLDAPIdentityProvider(t, idpv1alpha1.LDAPIdentityProviderSpec{
					Host: env.SupervisorUpstreamLDAP.Host,
					TLS: &idpv1alpha1.TLSSpec{
						CertificateAuthorityData: base64.StdEncoding.EncodeToString([]byte(env.SupervisorUpstreamLDAP.CABundle)),
					},
					Bind: idpv1alpha1.LDAPIdentityProviderBind{
						SecretName: secretName,
					},
					UserSearch: idpv1alpha1.LDAPIdentityProviderUserSearch{
						Base:   env.SupervisorUpstreamLDAP.UserSearchBase,
						Filter: "",
						Attributes: idpv1alpha1.LDAPIdentityProviderUserSearchAttributes{
							Username: env.SupervisorUpstreamLDAP.TestUserMailAttributeName,
							UID:      env.SupervisorUpstreamLDAP.TestUserUniqueIDAttributeName,
						},
					},
					GroupSearch: idpv1alpha1.LDAPIdentityProviderGroupSearch{
						Base:   env.SupervisorUpstreamLDAP.GroupSearchBase,
						Filter: "",
						Attributes: idpv1alpha1.LDAPIdentityProviderGroupSearchAttributes{
							GroupName: "dn",
						},
					},
				}, idpv1alpha1.LDAPPhaseReady)

				secret.Annotations = map[string]string{"pinniped.dev/test": "", "another-label": "another-key"}
				// update that secret, which will cause the cache to recheck tls and search base values
				client := testlib.NewKubernetesClientset(t)
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				updatedSecret, err := client.CoreV1().Secrets(env.SupervisorNamespace).Update(ctx, secret, metav1.UpdateOptions{})
				require.NoError(t, err)

				expectedMsg := fmt.Sprintf(
					`successfully able to connect to "%s" and bind as user "%s" [validated with Secret "%s" at version "%s"]`,
					env.SupervisorUpstreamLDAP.Host, env.SupervisorUpstreamLDAP.BindUsername,
					updatedSecret.Name, updatedSecret.ResourceVersion,
				)
				supervisorClient := testlib.NewSupervisorClientset(t)
				testlib.RequireEventually(t, func(requireEventually *require.Assertions) {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					ldapIDP, err = supervisorClient.IDPV1alpha1().LDAPIdentityProviders(env.SupervisorNamespace).Get(ctx, ldapIDP.Name, metav1.GetOptions{})
					requireEventually.NoError(err)
					requireEventuallySuccessfulLDAPIdentityProviderConditions(t, requireEventually, ldapIDP, expectedMsg)
				}, time.Minute, 500*time.Millisecond)
			},
			requestAuthorization: func(t *testing.T, downstreamAuthorizeURL, _ string, httpClient *http.Client) {
				requestAuthorizationUsingCLIPasswordFlow(t,
					downstreamAuthorizeURL,
					env.SupervisorUpstreamLDAP.TestUserMailAttributeValue, // username to present to server during login
					env.SupervisorUpstreamLDAP.TestUserPassword,           // password to present to server during login
					httpClient,
					false,
				)
			},
			// the ID token Subject should be the Host URL plus the value pulled from the requested UserSearch.Attributes.UID attribute
			wantDownstreamIDTokenSubjectToMatch: "^" + regexp.QuoteMeta(
				"ldaps://"+env.SupervisorUpstreamLDAP.Host+
					"?base="+url.QueryEscape(env.SupervisorUpstreamLDAP.UserSearchBase)+
					"&sub="+base64.RawURLEncoding.EncodeToString([]byte(env.SupervisorUpstreamLDAP.TestUserUniqueIDAttributeValue)),
			) + "$",
			// the ID token Username should have been pulled from the requested UserSearch.Attributes.Username attribute
			wantDownstreamIDTokenUsernameToMatch: "^" + regexp.QuoteMeta(env.SupervisorUpstreamLDAP.TestUserMailAttributeValue) + "$",
			wantDownstreamIDTokenGroups:          env.SupervisorUpstreamLDAP.TestUserDirectGroupsDNs,
		},
		{
			name: "ldap login still works after deleting and recreating the bind secret",
			maybeSkip: func(t *testing.T) {
				t.Helper()
				if len(env.ToolsNamespace) == 0 && !env.HasCapability(testlib.CanReachInternetLDAPPorts) {
					t.Skip("LDAP integration test requires connectivity to an LDAP server")
				}
			},
			createIDP: func(t *testing.T) {
				t.Helper()

				secret := testlib.CreateTestSecret(t, env.SupervisorNamespace, "ldap-service-account", v1.SecretTypeBasicAuth,
					map[string]string{
						v1.BasicAuthUsernameKey: env.SupervisorUpstreamLDAP.BindUsername,
						v1.BasicAuthPasswordKey: env.SupervisorUpstreamLDAP.BindPassword,
					},
				)
				secretName := secret.Name
				ldapIDP := testlib.CreateTestLDAPIdentityProvider(t, idpv1alpha1.LDAPIdentityProviderSpec{
					Host: env.SupervisorUpstreamLDAP.Host,
					TLS: &idpv1alpha1.TLSSpec{
						CertificateAuthorityData: base64.StdEncoding.EncodeToString([]byte(env.SupervisorUpstreamLDAP.CABundle)),
					},
					Bind: idpv1alpha1.LDAPIdentityProviderBind{
						SecretName: secretName,
					},
					UserSearch: idpv1alpha1.LDAPIdentityProviderUserSearch{
						Base:   env.SupervisorUpstreamLDAP.UserSearchBase,
						Filter: "",
						Attributes: idpv1alpha1.LDAPIdentityProviderUserSearchAttributes{
							Username: env.SupervisorUpstreamLDAP.TestUserMailAttributeName,
							UID:      env.SupervisorUpstreamLDAP.TestUserUniqueIDAttributeName,
						},
					},
					GroupSearch: idpv1alpha1.LDAPIdentityProviderGroupSearch{
						Base:   env.SupervisorUpstreamLDAP.GroupSearchBase,
						Filter: "",
						Attributes: idpv1alpha1.LDAPIdentityProviderGroupSearchAttributes{
							GroupName: "dn",
						},
					},
				}, idpv1alpha1.LDAPPhaseReady)

				// delete, then recreate that secret, which will cause the cache to recheck tls and search base values
				client := testlib.NewKubernetesClientset(t)
				deleteCtx, deleteCancel := context.WithTimeout(context.Background(), time.Minute)
				defer deleteCancel()
				err := client.CoreV1().Secrets(env.SupervisorNamespace).Delete(deleteCtx, secretName, metav1.DeleteOptions{})
				require.NoError(t, err)

				// create the secret again
				recreateCtx, recreateCancel := context.WithTimeout(context.Background(), time.Minute)
				defer recreateCancel()
				recreatedSecret, err := client.CoreV1().Secrets(env.SupervisorNamespace).Create(recreateCtx, &v1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      secretName,
						Namespace: env.SupervisorNamespace,
					},
					Type: v1.SecretTypeBasicAuth,
					StringData: map[string]string{
						v1.BasicAuthUsernameKey: env.SupervisorUpstreamLDAP.BindUsername,
						v1.BasicAuthPasswordKey: env.SupervisorUpstreamLDAP.BindPassword,
					},
				}, metav1.CreateOptions{})
				require.NoError(t, err)
				expectedMsg := fmt.Sprintf(
					`successfully able to connect to "%s" and bind as user "%s" [validated with Secret "%s" at version "%s"]`,
					env.SupervisorUpstreamLDAP.Host, env.SupervisorUpstreamLDAP.BindUsername,
					recreatedSecret.Name, recreatedSecret.ResourceVersion,
				)
				supervisorClient := testlib.NewSupervisorClientset(t)
				testlib.RequireEventually(t, func(requireEventually *require.Assertions) {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					ldapIDP, err = supervisorClient.IDPV1alpha1().LDAPIdentityProviders(env.SupervisorNamespace).Get(ctx, ldapIDP.Name, metav1.GetOptions{})
					requireEventually.NoError(err)
					requireEventuallySuccessfulLDAPIdentityProviderConditions(t, requireEventually, ldapIDP, expectedMsg)
				}, time.Minute, 500*time.Millisecond)
			},
			requestAuthorization: func(t *testing.T, downstreamAuthorizeURL, _ string, httpClient *http.Client) {
				requestAuthorizationUsingCLIPasswordFlow(t,
					downstreamAuthorizeURL,
					env.SupervisorUpstreamLDAP.TestUserMailAttributeValue, // username to present to server during login
					env.SupervisorUpstreamLDAP.TestUserPassword,           // password to present to server during login
					httpClient,
					false,
				)
			},
			// the ID token Subject should be the Host URL plus the value pulled from the requested UserSearch.Attributes.UID attribute
			wantDownstreamIDTokenSubjectToMatch: "^" + regexp.QuoteMeta(
				"ldaps://"+env.SupervisorUpstreamLDAP.Host+
					"?base="+url.QueryEscape(env.SupervisorUpstreamLDAP.UserSearchBase)+
					"&sub="+base64.RawURLEncoding.EncodeToString([]byte(env.SupervisorUpstreamLDAP.TestUserUniqueIDAttributeValue)),
			) + "$",
			// the ID token Username should have been pulled from the requested UserSearch.Attributes.Username attribute
			wantDownstreamIDTokenUsernameToMatch: "^" + regexp.QuoteMeta(env.SupervisorUpstreamLDAP.TestUserMailAttributeValue) + "$",
			wantDownstreamIDTokenGroups:          env.SupervisorUpstreamLDAP.TestUserDirectGroupsDNs,
		},
		{
			name: "activedirectory with all default options",
			maybeSkip: func(t *testing.T) {
				t.Helper()
				if len(env.ToolsNamespace) == 0 && !env.HasCapability(testlib.CanReachInternetLDAPPorts) {
					t.Skip("LDAP integration test requires connectivity to an LDAP server")
				}
				if env.SupervisorUpstreamActiveDirectory.Host == "" {
					t.Skip("Active Directory hostname not specified")
				}
			},
			createIDP: func(t *testing.T) {
				t.Helper()
				secret := testlib.CreateTestSecret(t, env.SupervisorNamespace, "ad-service-account", v1.SecretTypeBasicAuth,
					map[string]string{
						v1.BasicAuthUsernameKey: env.SupervisorUpstreamActiveDirectory.BindUsername,
						v1.BasicAuthPasswordKey: env.SupervisorUpstreamActiveDirectory.BindPassword,
					},
				)
				adIDP := testlib.CreateTestActiveDirectoryIdentityProvider(t, idpv1alpha1.ActiveDirectoryIdentityProviderSpec{
					Host: env.SupervisorUpstreamActiveDirectory.Host,
					TLS: &idpv1alpha1.TLSSpec{
						CertificateAuthorityData: base64.StdEncoding.EncodeToString([]byte(env.SupervisorUpstreamActiveDirectory.CABundle)),
					},
					Bind: idpv1alpha1.ActiveDirectoryIdentityProviderBind{
						SecretName: secret.Name,
					},
				}, idpv1alpha1.ActiveDirectoryPhaseReady)
				expectedMsg := fmt.Sprintf(
					`successfully able to connect to "%s" and bind as user "%s" [validated with Secret "%s" at version "%s"]`,
					env.SupervisorUpstreamActiveDirectory.Host, env.SupervisorUpstreamActiveDirectory.BindUsername,
					secret.Name, secret.ResourceVersion,
				)
				requireSuccessfulActiveDirectoryIdentityProviderConditions(t, adIDP, expectedMsg)
			},
			requestAuthorization: func(t *testing.T, downstreamAuthorizeURL, _ string, httpClient *http.Client) {
				requestAuthorizationUsingCLIPasswordFlow(t,
					downstreamAuthorizeURL,
					env.SupervisorUpstreamActiveDirectory.TestUserPrincipalNameValue, // username to present to server during login
					env.SupervisorUpstreamActiveDirectory.TestUserPassword,           // password to present to server during login
					httpClient,
					false,
				)
			},
			// the ID token Subject should be the Host URL plus the value pulled from the requested UserSearch.Attributes.UID attribute
			wantDownstreamIDTokenSubjectToMatch: "^" + regexp.QuoteMeta(
				"ldaps://"+env.SupervisorUpstreamActiveDirectory.Host+
					"?base="+url.QueryEscape(env.SupervisorUpstreamActiveDirectory.DefaultNamingContextSearchBase)+
					"&sub="+env.SupervisorUpstreamActiveDirectory.TestUserUniqueIDAttributeValue,
			) + "$",
			// the ID token Username should have been pulled from the requested UserSearch.Attributes.Username attribute
			wantDownstreamIDTokenUsernameToMatch: "^" + regexp.QuoteMeta(env.SupervisorUpstreamActiveDirectory.TestUserPrincipalNameValue) + "$",
			wantDownstreamIDTokenGroups:          env.SupervisorUpstreamActiveDirectory.TestUserIndirectGroupsSAMAccountPlusDomainNames,
		}, {
			name: "activedirectory with custom options",
			maybeSkip: func(t *testing.T) {
				t.Helper()
				if len(env.ToolsNamespace) == 0 && !env.HasCapability(testlib.CanReachInternetLDAPPorts) {
					t.Skip("LDAP integration test requires connectivity to an LDAP server")
				}
				if env.SupervisorUpstreamActiveDirectory.Host == "" {
					t.Skip("Active Directory hostname not specified")
				}
			},
			createIDP: func(t *testing.T) {
				t.Helper()
				secret := testlib.CreateTestSecret(t, env.SupervisorNamespace, "ad-service-account", v1.SecretTypeBasicAuth,
					map[string]string{
						v1.BasicAuthUsernameKey: env.SupervisorUpstreamActiveDirectory.BindUsername,
						v1.BasicAuthPasswordKey: env.SupervisorUpstreamActiveDirectory.BindPassword,
					},
				)
				adIDP := testlib.CreateTestActiveDirectoryIdentityProvider(t, idpv1alpha1.ActiveDirectoryIdentityProviderSpec{
					Host: env.SupervisorUpstreamActiveDirectory.Host,
					TLS: &idpv1alpha1.TLSSpec{
						CertificateAuthorityData: base64.StdEncoding.EncodeToString([]byte(env.SupervisorUpstreamActiveDirectory.CABundle)),
					},
					Bind: idpv1alpha1.ActiveDirectoryIdentityProviderBind{
						SecretName: secret.Name,
					},
					UserSearch: idpv1alpha1.ActiveDirectoryIdentityProviderUserSearch{
						Base:   env.SupervisorUpstreamActiveDirectory.UserSearchBase,
						Filter: env.SupervisorUpstreamActiveDirectory.TestUserMailAttributeName + "={}",
						Attributes: idpv1alpha1.ActiveDirectoryIdentityProviderUserSearchAttributes{
							Username: env.SupervisorUpstreamActiveDirectory.TestUserMailAttributeName,
						},
					},
					GroupSearch: idpv1alpha1.ActiveDirectoryIdentityProviderGroupSearch{
						Filter: "member={}", // excluding nested groups
						Base:   env.SupervisorUpstreamActiveDirectory.GroupSearchBase,
						Attributes: idpv1alpha1.ActiveDirectoryIdentityProviderGroupSearchAttributes{
							GroupName: "dn",
						},
					},
				}, idpv1alpha1.ActiveDirectoryPhaseReady)
				expectedMsg := fmt.Sprintf(
					`successfully able to connect to "%s" and bind as user "%s" [validated with Secret "%s" at version "%s"]`,
					env.SupervisorUpstreamActiveDirectory.Host, env.SupervisorUpstreamActiveDirectory.BindUsername,
					secret.Name, secret.ResourceVersion,
				)
				requireSuccessfulActiveDirectoryIdentityProviderConditions(t, adIDP, expectedMsg)
			},
			requestAuthorization: func(t *testing.T, downstreamAuthorizeURL, _ string, httpClient *http.Client) {
				requestAuthorizationUsingCLIPasswordFlow(t,
					downstreamAuthorizeURL,
					env.SupervisorUpstreamActiveDirectory.TestUserMailAttributeValue, // username to present to server during login
					env.SupervisorUpstreamActiveDirectory.TestUserPassword,           // password to present to server during login
					httpClient,
					false,
				)
			},
			// the ID token Subject should be the Host URL plus the value pulled from the requested UserSearch.Attributes.UID attribute
			wantDownstreamIDTokenSubjectToMatch: "^" + regexp.QuoteMeta(
				"ldaps://"+env.SupervisorUpstreamActiveDirectory.Host+
					"?base="+url.QueryEscape(env.SupervisorUpstreamActiveDirectory.UserSearchBase)+
					"&sub="+env.SupervisorUpstreamActiveDirectory.TestUserUniqueIDAttributeValue,
			) + "$",
			// the ID token Username should have been pulled from the requested UserSearch.Attributes.Username attribute
			wantDownstreamIDTokenUsernameToMatch: "^" + regexp.QuoteMeta(env.SupervisorUpstreamActiveDirectory.TestUserMailAttributeValue) + "$",
			wantDownstreamIDTokenGroups:          env.SupervisorUpstreamActiveDirectory.TestUserDirectGroupsDNs,
		},
		{
			name: "active directory login still works after updating bind secret",
			maybeSkip: func(t *testing.T) {
				t.Helper()
				if len(env.ToolsNamespace) == 0 && !env.HasCapability(testlib.CanReachInternetLDAPPorts) {
					t.Skip("LDAP integration test requires connectivity to an LDAP server")
				}
				if env.SupervisorUpstreamActiveDirectory.Host == "" {
					t.Skip("Active Directory hostname not specified")
				}
			},
			createIDP: func(t *testing.T) {
				t.Helper()

				secret := testlib.CreateTestSecret(t, env.SupervisorNamespace, "ad-service-account", v1.SecretTypeBasicAuth,
					map[string]string{
						v1.BasicAuthUsernameKey: env.SupervisorUpstreamActiveDirectory.BindUsername,
						v1.BasicAuthPasswordKey: env.SupervisorUpstreamActiveDirectory.BindPassword,
					},
				)
				secretName := secret.Name
				adIDP := testlib.CreateTestActiveDirectoryIdentityProvider(t, idpv1alpha1.ActiveDirectoryIdentityProviderSpec{
					Host: env.SupervisorUpstreamActiveDirectory.Host,
					TLS: &idpv1alpha1.TLSSpec{
						CertificateAuthorityData: base64.StdEncoding.EncodeToString([]byte(env.SupervisorUpstreamActiveDirectory.CABundle)),
					},
					Bind: idpv1alpha1.ActiveDirectoryIdentityProviderBind{
						SecretName: secretName,
					},
				}, idpv1alpha1.ActiveDirectoryPhaseReady)

				secret.Annotations = map[string]string{"pinniped.dev/test": "", "another-label": "another-key"}
				// update that secret, which will cause the cache to recheck tls and search base values
				client := testlib.NewKubernetesClientset(t)
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				updatedSecret, err := client.CoreV1().Secrets(env.SupervisorNamespace).Update(ctx, secret, metav1.UpdateOptions{})
				require.NoError(t, err)

				expectedMsg := fmt.Sprintf(
					`successfully able to connect to "%s" and bind as user "%s" [validated with Secret "%s" at version "%s"]`,
					env.SupervisorUpstreamActiveDirectory.Host, env.SupervisorUpstreamActiveDirectory.BindUsername,
					updatedSecret.Name, updatedSecret.ResourceVersion,
				)
				supervisorClient := testlib.NewSupervisorClientset(t)
				testlib.RequireEventually(t, func(requireEventually *require.Assertions) {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					adIDP, err = supervisorClient.IDPV1alpha1().ActiveDirectoryIdentityProviders(env.SupervisorNamespace).Get(ctx, adIDP.Name, metav1.GetOptions{})
					requireEventually.NoError(err)
					requireEventuallySuccessfulActiveDirectoryIdentityProviderConditions(t, requireEventually, adIDP, expectedMsg)
				}, time.Minute, 500*time.Millisecond)
			},
			requestAuthorization: func(t *testing.T, downstreamAuthorizeURL, _ string, httpClient *http.Client) {
				requestAuthorizationUsingCLIPasswordFlow(t,
					downstreamAuthorizeURL,
					env.SupervisorUpstreamActiveDirectory.TestUserPrincipalNameValue, // username to present to server during login
					env.SupervisorUpstreamActiveDirectory.TestUserPassword,           // password to present to server during login
					httpClient,
					false,
				)
			},
			// the ID token Subject should be the Host URL plus the value pulled from the requested UserSearch.Attributes.UID attribute
			wantDownstreamIDTokenSubjectToMatch: "^" + regexp.QuoteMeta(
				"ldaps://"+env.SupervisorUpstreamActiveDirectory.Host+
					"?base="+url.QueryEscape(env.SupervisorUpstreamActiveDirectory.DefaultNamingContextSearchBase)+
					"&sub="+env.SupervisorUpstreamActiveDirectory.TestUserUniqueIDAttributeValue,
			) + "$",
			// the ID token Username should have been pulled from the requested UserSearch.Attributes.Username attribute
			wantDownstreamIDTokenUsernameToMatch: "^" + regexp.QuoteMeta(env.SupervisorUpstreamActiveDirectory.TestUserPrincipalNameValue) + "$",
			wantDownstreamIDTokenGroups:          env.SupervisorUpstreamActiveDirectory.TestUserIndirectGroupsSAMAccountPlusDomainNames,
		},
		{
			name: "active directory login still works after deleting and recreating bind secret",
			maybeSkip: func(t *testing.T) {
				t.Helper()
				if len(env.ToolsNamespace) == 0 && !env.HasCapability(testlib.CanReachInternetLDAPPorts) {
					t.Skip("LDAP integration test requires connectivity to an LDAP server")
				}
				if env.SupervisorUpstreamActiveDirectory.Host == "" {
					t.Skip("Active Directory hostname not specified")
				}
			},
			createIDP: func(t *testing.T) {
				t.Helper()

				secret := testlib.CreateTestSecret(t, env.SupervisorNamespace, "ad-service-account", v1.SecretTypeBasicAuth,
					map[string]string{
						v1.BasicAuthUsernameKey: env.SupervisorUpstreamActiveDirectory.BindUsername,
						v1.BasicAuthPasswordKey: env.SupervisorUpstreamActiveDirectory.BindPassword,
					},
				)
				secretName := secret.Name
				adIDP := testlib.CreateTestActiveDirectoryIdentityProvider(t, idpv1alpha1.ActiveDirectoryIdentityProviderSpec{
					Host: env.SupervisorUpstreamActiveDirectory.Host,
					TLS: &idpv1alpha1.TLSSpec{
						CertificateAuthorityData: base64.StdEncoding.EncodeToString([]byte(env.SupervisorUpstreamActiveDirectory.CABundle)),
					},
					Bind: idpv1alpha1.ActiveDirectoryIdentityProviderBind{
						SecretName: secretName,
					},
				}, idpv1alpha1.ActiveDirectoryPhaseReady)

				// delete the secret
				client := testlib.NewKubernetesClientset(t)
				deleteCtx, deleteCancel := context.WithTimeout(context.Background(), time.Minute)
				defer deleteCancel()
				err := client.CoreV1().Secrets(env.SupervisorNamespace).Delete(deleteCtx, secretName, metav1.DeleteOptions{})
				require.NoError(t, err)

				// create the secret again
				recreateCtx, recreateCancel := context.WithTimeout(context.Background(), time.Minute)
				defer recreateCancel()
				recreatedSecret, err := client.CoreV1().Secrets(env.SupervisorNamespace).Create(recreateCtx, &v1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      secretName,
						Namespace: env.SupervisorNamespace,
					},
					Type: v1.SecretTypeBasicAuth,
					StringData: map[string]string{
						v1.BasicAuthUsernameKey: env.SupervisorUpstreamActiveDirectory.BindUsername,
						v1.BasicAuthPasswordKey: env.SupervisorUpstreamActiveDirectory.BindPassword,
					},
				}, metav1.CreateOptions{})
				require.NoError(t, err)

				expectedMsg := fmt.Sprintf(
					`successfully able to connect to "%s" and bind as user "%s" [validated with Secret "%s" at version "%s"]`,
					env.SupervisorUpstreamActiveDirectory.Host, env.SupervisorUpstreamActiveDirectory.BindUsername,
					recreatedSecret.Name, recreatedSecret.ResourceVersion,
				)
				supervisorClient := testlib.NewSupervisorClientset(t)
				testlib.RequireEventually(t, func(requireEventually *require.Assertions) {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					adIDP, err = supervisorClient.IDPV1alpha1().ActiveDirectoryIdentityProviders(env.SupervisorNamespace).Get(ctx, adIDP.Name, metav1.GetOptions{})
					requireEventually.NoError(err)
					requireEventuallySuccessfulActiveDirectoryIdentityProviderConditions(t, requireEventually, adIDP, expectedMsg)
				}, time.Minute, 500*time.Millisecond)
			},
			requestAuthorization: func(t *testing.T, downstreamAuthorizeURL, _ string, httpClient *http.Client) {
				requestAuthorizationUsingCLIPasswordFlow(t,
					downstreamAuthorizeURL,
					env.SupervisorUpstreamActiveDirectory.TestUserPrincipalNameValue, // username to present to server during login
					env.SupervisorUpstreamActiveDirectory.TestUserPassword,           // password to present to server during login
					httpClient,
					false,
				)
			},
			// the ID token Subject should be the Host URL plus the value pulled from the requested UserSearch.Attributes.UID attribute
			wantDownstreamIDTokenSubjectToMatch: "^" + regexp.QuoteMeta(
				"ldaps://"+env.SupervisorUpstreamActiveDirectory.Host+
					"?base="+url.QueryEscape(env.SupervisorUpstreamActiveDirectory.DefaultNamingContextSearchBase)+
					"&sub="+env.SupervisorUpstreamActiveDirectory.TestUserUniqueIDAttributeValue,
			) + "$",
			// the ID token Username should have been pulled from the requested UserSearch.Attributes.Username attribute
			wantDownstreamIDTokenUsernameToMatch: "^" + regexp.QuoteMeta(env.SupervisorUpstreamActiveDirectory.TestUserPrincipalNameValue) + "$",
			wantDownstreamIDTokenGroups:          env.SupervisorUpstreamActiveDirectory.TestUserIndirectGroupsSAMAccountPlusDomainNames,
		},
		{
			name: "logging in to activedirectory with a deactivated user fails",
			maybeSkip: func(t *testing.T) {
				t.Helper()
				if len(env.ToolsNamespace) == 0 && !env.HasCapability(testlib.CanReachInternetLDAPPorts) {
					t.Skip("LDAP integration test requires connectivity to an LDAP server")
				}
				if env.SupervisorUpstreamActiveDirectory.Host == "" {
					t.Skip("Active Directory hostname not specified")
				}
			},
			createIDP: func(t *testing.T) {
				t.Helper()
				secret := testlib.CreateTestSecret(t, env.SupervisorNamespace, "ad-service-account", v1.SecretTypeBasicAuth,
					map[string]string{
						v1.BasicAuthUsernameKey: env.SupervisorUpstreamActiveDirectory.BindUsername,
						v1.BasicAuthPasswordKey: env.SupervisorUpstreamActiveDirectory.BindPassword,
					},
				)
				adIDP := testlib.CreateTestActiveDirectoryIdentityProvider(t, idpv1alpha1.ActiveDirectoryIdentityProviderSpec{
					Host: env.SupervisorUpstreamActiveDirectory.Host,
					TLS: &idpv1alpha1.TLSSpec{
						CertificateAuthorityData: base64.StdEncoding.EncodeToString([]byte(env.SupervisorUpstreamActiveDirectory.CABundle)),
					},
					Bind: idpv1alpha1.ActiveDirectoryIdentityProviderBind{
						SecretName: secret.Name,
					},
				}, idpv1alpha1.ActiveDirectoryPhaseReady)
				expectedMsg := fmt.Sprintf(
					`successfully able to connect to "%s" and bind as user "%s" [validated with Secret "%s" at version "%s"]`,
					env.SupervisorUpstreamActiveDirectory.Host, env.SupervisorUpstreamActiveDirectory.BindUsername,
					secret.Name, secret.ResourceVersion,
				)
				requireSuccessfulActiveDirectoryIdentityProviderConditions(t, adIDP, expectedMsg)
			},
			requestAuthorization: func(t *testing.T, downstreamAuthorizeURL, _ string, httpClient *http.Client) {
				requestAuthorizationUsingCLIPasswordFlow(t,
					downstreamAuthorizeURL,
					env.SupervisorUpstreamActiveDirectory.TestDeactivatedUserSAMAccountNameValue, // username to present to server during login
					env.SupervisorUpstreamActiveDirectory.TestDeactivatedUserPassword,            // password to present to server during login
					httpClient,
					true,
				)
			},
			wantErrorDescription: "The resource owner or authorization server denied the request. Username/password not accepted by LDAP provider.",
			wantErrorType:        "access_denied",
		},
	}
	for _, test := range tests {
		tt := test
		t.Run(tt.name, func(t *testing.T) {
			tt.maybeSkip(t)

			testSupervisorLogin(t,
				tt.createIDP,
				tt.requestAuthorization,
				tt.wantDownstreamIDTokenSubjectToMatch,
				tt.wantDownstreamIDTokenUsernameToMatch,
				tt.wantDownstreamIDTokenGroups,
				tt.wantErrorDescription, tt.wantErrorType,
			)
		})
	}
}

func requireSuccessfulLDAPIdentityProviderConditions(t *testing.T, ldapIDP *idpv1alpha1.LDAPIdentityProvider, expectedLDAPConnectionValidMessage string) {
	require.Len(t, ldapIDP.Status.Conditions, 3)

	conditionsSummary := [][]string{}
	for _, condition := range ldapIDP.Status.Conditions {
		conditionsSummary = append(conditionsSummary, []string{condition.Type, string(condition.Status), condition.Reason})
		t.Logf("Saw LDAPIdentityProvider Status.Condition Type=%s Status=%s Reason=%s Message=%s",
			condition.Type, string(condition.Status), condition.Reason, condition.Message)
		switch condition.Type {
		case "BindSecretValid":
			require.Equal(t, "loaded bind secret", condition.Message)
		case "TLSConfigurationValid":
			require.Equal(t, "loaded TLS configuration", condition.Message)
		case "LDAPConnectionValid":
			require.Equal(t, expectedLDAPConnectionValidMessage, condition.Message)
		}
	}

	require.ElementsMatch(t, [][]string{
		{"BindSecretValid", "True", "Success"},
		{"TLSConfigurationValid", "True", "Success"},
		{"LDAPConnectionValid", "True", "Success"},
	}, conditionsSummary)
}
func requireSuccessfulActiveDirectoryIdentityProviderConditions(t *testing.T, adIDP *idpv1alpha1.ActiveDirectoryIdentityProvider, expectedActiveDirectoryConnectionValidMessage string) {
	require.Len(t, adIDP.Status.Conditions, 4)

	conditionsSummary := [][]string{}
	for _, condition := range adIDP.Status.Conditions {
		conditionsSummary = append(conditionsSummary, []string{condition.Type, string(condition.Status), condition.Reason})
		t.Logf("Saw ActiveDirectoryIdentityProvider Status.Condition Type=%s Status=%s Reason=%s Message=%s",
			condition.Type, string(condition.Status), condition.Reason, condition.Message)
		switch condition.Type {
		case "BindSecretValid":
			require.Equal(t, "loaded bind secret", condition.Message)
		case "TLSConfigurationValid":
			require.Equal(t, "loaded TLS configuration", condition.Message)
		case "LDAPConnectionValid":
			require.Equal(t, expectedActiveDirectoryConnectionValidMessage, condition.Message)
		}
	}

	expectedUserSearchReason := ""
	if adIDP.Spec.UserSearch.Base == "" || adIDP.Spec.GroupSearch.Base == "" {
		expectedUserSearchReason = "Success"
	} else {
		expectedUserSearchReason = "UsingConfigurationFromSpec"
	}

	require.ElementsMatch(t, [][]string{
		{"BindSecretValid", "True", "Success"},
		{"TLSConfigurationValid", "True", "Success"},
		{"LDAPConnectionValid", "True", "Success"},
		{"SearchBaseFound", "True", expectedUserSearchReason},
	}, conditionsSummary)
}

func requireEventuallySuccessfulLDAPIdentityProviderConditions(t *testing.T, requireEventually *require.Assertions, ldapIDP *idpv1alpha1.LDAPIdentityProvider, expectedLDAPConnectionValidMessage string) {
	t.Helper()
	requireEventually.Len(ldapIDP.Status.Conditions, 3)

	conditionsSummary := [][]string{}
	for _, condition := range ldapIDP.Status.Conditions {
		conditionsSummary = append(conditionsSummary, []string{condition.Type, string(condition.Status), condition.Reason})
		t.Logf("Saw ActiveDirectoryIdentityProvider Status.Condition Type=%s Status=%s Reason=%s Message=%s",
			condition.Type, string(condition.Status), condition.Reason, condition.Message)
		switch condition.Type {
		case "BindSecretValid":
			requireEventually.Equal("loaded bind secret", condition.Message)
		case "TLSConfigurationValid":
			requireEventually.Equal("loaded TLS configuration", condition.Message)
		case "LDAPConnectionValid":
			requireEventually.Equal(expectedLDAPConnectionValidMessage, condition.Message)
		}
	}

	requireEventually.ElementsMatch([][]string{
		{"BindSecretValid", "True", "Success"},
		{"TLSConfigurationValid", "True", "Success"},
		{"LDAPConnectionValid", "True", "Success"},
	}, conditionsSummary)
}

func requireEventuallySuccessfulActiveDirectoryIdentityProviderConditions(t *testing.T, requireEventually *require.Assertions, adIDP *idpv1alpha1.ActiveDirectoryIdentityProvider, expectedActiveDirectoryConnectionValidMessage string) {
	t.Helper()
	requireEventually.Len(adIDP.Status.Conditions, 4)

	conditionsSummary := [][]string{}
	for _, condition := range adIDP.Status.Conditions {
		conditionsSummary = append(conditionsSummary, []string{condition.Type, string(condition.Status), condition.Reason})
		t.Logf("Saw ActiveDirectoryIdentityProvider Status.Condition Type=%s Status=%s Reason=%s Message=%s",
			condition.Type, string(condition.Status), condition.Reason, condition.Message)
		switch condition.Type {
		case "BindSecretValid":
			requireEventually.Equal("loaded bind secret", condition.Message)
		case "TLSConfigurationValid":
			requireEventually.Equal("loaded TLS configuration", condition.Message)
		case "LDAPConnectionValid":
			requireEventually.Equal(expectedActiveDirectoryConnectionValidMessage, condition.Message)
		}
	}

	expectedUserSearchReason := ""
	if adIDP.Spec.UserSearch.Base == "" || adIDP.Spec.GroupSearch.Base == "" {
		expectedUserSearchReason = "Success"
	} else {
		expectedUserSearchReason = "UsingConfigurationFromSpec"
	}

	requireEventually.ElementsMatch([][]string{
		{"BindSecretValid", "True", "Success"},
		{"TLSConfigurationValid", "True", "Success"},
		{"LDAPConnectionValid", "True", "Success"},
		{"SearchBaseFound", "True", expectedUserSearchReason},
	}, conditionsSummary)
}

func testSupervisorLogin(
	t *testing.T,
	createIDP func(t *testing.T),
	requestAuthorization func(t *testing.T, downstreamAuthorizeURL, downstreamCallbackURL string, httpClient *http.Client),
	wantDownstreamIDTokenSubjectToMatch, wantDownstreamIDTokenUsernameToMatch string, wantDownstreamIDTokenGroups []string,
	wantErrorDescription string, wantErrorType string,
) {
	env := testlib.IntegrationEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Infer the downstream issuer URL from the callback associated with the upstream test client registration.
	issuerURL, err := url.Parse(env.SupervisorUpstreamOIDC.CallbackURL)
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(issuerURL.Path, "/callback"))
	issuerURL.Path = strings.TrimSuffix(issuerURL.Path, "/callback")
	t.Logf("testing with downstream issuer URL %s", issuerURL.String())

	// Generate a CA bundle with which to serve this provider.
	t.Logf("generating test CA")
	ca, err := certauthority.New("Downstream Test CA", 1*time.Hour)
	require.NoError(t, err)

	// Create an HTTP client that can reach the downstream discovery endpoint using the CA certs.
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: ca.Pool()},
			Proxy: func(req *http.Request) (*url.URL, error) {
				if strings.HasPrefix(req.URL.Host, "127.0.0.1") {
					// don't proxy requests to localhost to avoid proxying calls to our local callback listener
					return nil, nil
				}
				if env.Proxy == "" {
					t.Logf("passing request for %s with no proxy", testlib.RedactURLParams(req.URL))
					return nil, nil
				}
				proxyURL, err := url.Parse(env.Proxy)
				require.NoError(t, err)
				t.Logf("passing request for %s through proxy %s", testlib.RedactURLParams(req.URL), proxyURL.String())
				return proxyURL, nil
			},
		},
		// Don't follow redirects automatically.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	oidcHTTPClientContext := coreosoidc.ClientContext(ctx, httpClient)

	// Use the CA to issue a TLS server cert.
	t.Logf("issuing test certificate")
	tlsCert, err := ca.IssueServerCert([]string{issuerURL.Hostname()}, nil, 1*time.Hour)
	require.NoError(t, err)
	certPEM, keyPEM, err := certauthority.ToPEM(tlsCert)
	require.NoError(t, err)

	// Write the serving cert to a secret.
	certSecret := testlib.CreateTestSecret(t,
		env.SupervisorNamespace,
		"oidc-provider-tls",
		v1.SecretTypeTLS,
		map[string]string{"tls.crt": string(certPEM), "tls.key": string(keyPEM)},
	)

	// Create the downstream FederationDomain and expect it to go into the success status condition.
	downstream := testlib.CreateTestFederationDomain(ctx, t,
		issuerURL.String(),
		certSecret.Name,
		configv1alpha1.SuccessFederationDomainStatusCondition,
	)

	// Ensure the the JWKS data is created and ready for the new FederationDomain by waiting for
	// the `/jwks.json` endpoint to succeed, because there is no point in proceeding and eventually
	// calling the token endpoint from this test until the JWKS data has been loaded into
	// the server's in-memory JWKS cache for the token endpoint to use.
	requestJWKSEndpoint, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/jwks.json", issuerURL.String()),
		nil,
	)
	require.NoError(t, err)
	testlib.RequireEventually(t, func(requireEventually *require.Assertions) {
		rsp, err := httpClient.Do(requestJWKSEndpoint)
		requireEventually.NoError(err)
		requireEventually.NoError(rsp.Body.Close())
		requireEventually.Equal(http.StatusOK, rsp.StatusCode)
	}, 30*time.Second, 200*time.Millisecond)

	// Create upstream IDP and wait for it to become ready.
	createIDP(t)

	// Perform OIDC discovery for our downstream.
	var discovery *coreosoidc.Provider
	testlib.RequireEventually(t, func(requireEventually *require.Assertions) {
		var err error
		discovery, err = coreosoidc.NewProvider(oidcHTTPClientContext, downstream.Spec.Issuer)
		requireEventually.NoError(err)
	}, 30*time.Second, 200*time.Millisecond)

	// Start a callback server on localhost.
	localCallbackServer := startLocalCallbackServer(t)

	// Form the OAuth2 configuration corresponding to our CLI client.
	downstreamOAuth2Config := oauth2.Config{
		// This is the hardcoded public client that the supervisor supports.
		ClientID:    "pinniped-cli",
		Endpoint:    discovery.Endpoint(),
		RedirectURL: localCallbackServer.URL,
		Scopes:      []string{"openid", "pinniped:request-audience", "offline_access"},
	}

	// Build a valid downstream authorize URL for the supervisor.
	stateParam, err := state.Generate()
	require.NoError(t, err)
	nonceParam, err := nonce.Generate()
	require.NoError(t, err)
	pkceParam, err := pkce.Generate()
	require.NoError(t, err)
	downstreamAuthorizeURL := downstreamOAuth2Config.AuthCodeURL(
		stateParam.String(),
		nonceParam.Param(),
		pkceParam.Challenge(),
		pkceParam.Method(),
	)

	// Perform parameterized auth code acquisition.
	requestAuthorization(t, downstreamAuthorizeURL, localCallbackServer.URL, httpClient)

	// Expect that our callback handler was invoked.
	callback := localCallbackServer.waitForCallback(10 * time.Second)
	t.Logf("got callback request: %s", testlib.MaskTokens(callback.URL.String()))
	if wantErrorType == "" {
		require.Equal(t, stateParam.String(), callback.URL.Query().Get("state"))
		require.ElementsMatch(t, []string{"openid", "pinniped:request-audience", "offline_access"}, strings.Split(callback.URL.Query().Get("scope"), " "))
		authcode := callback.URL.Query().Get("code")
		require.NotEmpty(t, authcode)

		// Call the token endpoint to get tokens.
		tokenResponse, err := downstreamOAuth2Config.Exchange(oidcHTTPClientContext, authcode, pkceParam.Verifier())
		require.NoError(t, err)

		expectedIDTokenClaims := []string{"iss", "exp", "sub", "aud", "auth_time", "iat", "jti", "nonce", "rat", "username", "groups"}
		verifyTokenResponse(t,
			tokenResponse, discovery, downstreamOAuth2Config, nonceParam,
			expectedIDTokenClaims, wantDownstreamIDTokenSubjectToMatch, wantDownstreamIDTokenUsernameToMatch, wantDownstreamIDTokenGroups)

		// token exchange on the original token
		doTokenExchange(t, &downstreamOAuth2Config, tokenResponse, httpClient, discovery)

		// Use the refresh token to get new tokens
		refreshSource := downstreamOAuth2Config.TokenSource(oidcHTTPClientContext, &oauth2.Token{RefreshToken: tokenResponse.RefreshToken})
		refreshedTokenResponse, err := refreshSource.Token()
		require.NoError(t, err)

		// When refreshing, expect to get an "at_hash" claim, but no "nonce" claim.
		expectRefreshedIDTokenClaims := []string{"iss", "exp", "sub", "aud", "auth_time", "iat", "jti", "rat", "username", "groups", "at_hash"}
		verifyTokenResponse(t,
			refreshedTokenResponse, discovery, downstreamOAuth2Config, "",
			expectRefreshedIDTokenClaims, wantDownstreamIDTokenSubjectToMatch, wantDownstreamIDTokenUsernameToMatch, wantDownstreamIDTokenGroups)

		require.NotEqual(t, tokenResponse.AccessToken, refreshedTokenResponse.AccessToken)
		require.NotEqual(t, tokenResponse.RefreshToken, refreshedTokenResponse.RefreshToken)
		require.NotEqual(t, tokenResponse.Extra("id_token"), refreshedTokenResponse.Extra("id_token"))

		// token exchange on the refreshed token
		doTokenExchange(t, &downstreamOAuth2Config, refreshedTokenResponse, httpClient, discovery)
	} else {
		errorDescription := callback.URL.Query().Get("error_description")
		errorType := callback.URL.Query().Get("error")
		require.Equal(t, errorDescription, wantErrorDescription)
		require.Equal(t, errorType, wantErrorType)
	}
}

func verifyTokenResponse(
	t *testing.T,
	tokenResponse *oauth2.Token,
	discovery *coreosoidc.Provider,
	downstreamOAuth2Config oauth2.Config,
	nonceParam nonce.Nonce,
	expectedIDTokenClaims []string,
	wantDownstreamIDTokenSubjectToMatch, wantDownstreamIDTokenUsernameToMatch string, wantDownstreamIDTokenGroups []string,
) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Verify the ID Token.
	rawIDToken, ok := tokenResponse.Extra("id_token").(string)
	require.True(t, ok, "expected to get an ID token but did not")
	var verifier = discovery.Verifier(&coreosoidc.Config{ClientID: downstreamOAuth2Config.ClientID})
	idToken, err := verifier.Verify(ctx, rawIDToken)
	require.NoError(t, err)

	// Check the sub claim of the ID token.
	require.Regexp(t, wantDownstreamIDTokenSubjectToMatch, idToken.Subject)

	// Check the nonce claim of the ID token.
	require.NoError(t, nonceParam.Validate(idToken))

	// Check the exp claim of the ID token.
	expectedIDTokenLifetime := oidc.DefaultOIDCTimeoutsConfiguration().IDTokenLifespan
	testutil.RequireTimeInDelta(t, time.Now().UTC().Add(expectedIDTokenLifetime), idToken.Expiry, time.Second*30)

	// Check the full list of claim names of the ID token.
	idTokenClaims := map[string]interface{}{}
	err = idToken.Claims(&idTokenClaims)
	require.NoError(t, err)
	idTokenClaimNames := []string{}
	for k := range idTokenClaims {
		idTokenClaimNames = append(idTokenClaimNames, k)
	}
	require.ElementsMatch(t, expectedIDTokenClaims, idTokenClaimNames)

	// Check username claim of the ID token.
	require.Regexp(t, wantDownstreamIDTokenUsernameToMatch, idTokenClaims["username"].(string))

	// Check the groups claim.
	require.ElementsMatch(t, wantDownstreamIDTokenGroups, idTokenClaims["groups"])

	// Some light verification of the other tokens that were returned.
	require.NotEmpty(t, tokenResponse.AccessToken)
	require.Equal(t, "bearer", tokenResponse.TokenType)
	require.NotZero(t, tokenResponse.Expiry)
	expectedAccessTokenLifetime := oidc.DefaultOIDCTimeoutsConfiguration().AccessTokenLifespan
	testutil.RequireTimeInDelta(t, time.Now().UTC().Add(expectedAccessTokenLifetime), tokenResponse.Expiry, time.Second*30)

	require.NotEmpty(t, tokenResponse.RefreshToken)
}

func requestAuthorizationUsingBrowserAuthcodeFlow(t *testing.T, downstreamAuthorizeURL, downstreamCallbackURL string, httpClient *http.Client) {
	t.Helper()
	env := testlib.IntegrationEnv(t)

	ctx, cancelFunc := context.WithTimeout(context.Background(), time.Minute)
	defer cancelFunc()

	// Make the authorize request once "manually" so we can check its response security headers.
	authorizeRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, downstreamAuthorizeURL, nil)
	require.NoError(t, err)
	authorizeResp, err := httpClient.Do(authorizeRequest)
	require.NoError(t, err)
	require.NoError(t, authorizeResp.Body.Close())
	expectSecurityHeaders(t, authorizeResp, false)

	// Open the web browser and navigate to the downstream authorize URL.
	page := browsertest.Open(t)
	t.Logf("opening browser to downstream authorize URL %s", testlib.MaskTokens(downstreamAuthorizeURL))
	require.NoError(t, page.Navigate(downstreamAuthorizeURL))

	// Expect to be redirected to the upstream provider and log in.
	browsertest.LoginToUpstream(t, page, env.SupervisorUpstreamOIDC)

	// Wait for the login to happen and us be redirected back to a localhost callback.
	t.Logf("waiting for redirect to callback")
	callbackURLPattern := regexp.MustCompile(`\A` + regexp.QuoteMeta(downstreamCallbackURL) + `\?.+\z`)
	browsertest.WaitForURL(t, page, callbackURLPattern)
}

func requestAuthorizationUsingCLIPasswordFlow(t *testing.T, downstreamAuthorizeURL, upstreamUsername, upstreamPassword string, httpClient *http.Client, wantErr bool) {
	t.Helper()

	ctx, cancelFunc := context.WithTimeout(context.Background(), time.Minute)
	defer cancelFunc()

	authRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, downstreamAuthorizeURL, nil)
	require.NoError(t, err)

	// Set the custom username/password headers for the LDAP authorize request.
	authRequest.Header.Set("Pinniped-Username", upstreamUsername)
	authRequest.Header.Set("Pinniped-Password", upstreamPassword)

	// At this point in the test, we've already waited for the LDAPIdentityProvider to be loaded and marked healthy by
	// at least one Supervisor pod, but we can't be sure that _all_ of them have loaded the provider, so we may need
	// to retry this request multiple times until we get the expected 302 status response.
	var authResponse *http.Response
	var responseBody []byte
	testlib.RequireEventuallyWithoutError(t, func() (bool, error) {
		authResponse, err = httpClient.Do(authRequest)
		if err != nil {
			t.Logf("got authorization response with error %v", err)
			return false, nil
		}
		defer func() { _ = authResponse.Body.Close() }()
		responseBody, err = ioutil.ReadAll(authResponse.Body)
		if err != nil {
			return false, nil
		}
		t.Logf("got authorization response with code %d (%d byte body)", authResponse.StatusCode, len(responseBody))
		if authResponse.StatusCode != http.StatusFound {
			return false, nil
		}
		return true, nil
	}, 60*time.Second, 200*time.Millisecond)

	expectSecurityHeaders(t, authResponse, true)

	// A successful authorize request results in a redirect to our localhost callback listener with an authcode param.
	require.Equalf(t, http.StatusFound, authResponse.StatusCode, "response body was: %s", string(responseBody))
	redirectLocation := authResponse.Header.Get("Location")
	require.Contains(t, redirectLocation, "127.0.0.1")
	require.Contains(t, redirectLocation, "/callback")
	if wantErr {
		require.Contains(t, redirectLocation, "error_description")
	} else {
		require.Contains(t, redirectLocation, "code=")
	}

	// Follow the redirect.
	callbackRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, redirectLocation, nil)
	require.NoError(t, err)

	// Our localhost callback listener should have returned 200 OK.
	callbackResponse, err := httpClient.Do(callbackRequest)
	require.NoError(t, err)
	defer callbackResponse.Body.Close()
	require.Equal(t, http.StatusOK, callbackResponse.StatusCode)
}

func startLocalCallbackServer(t *testing.T) *localCallbackServer {
	// Handle the callback by sending the *http.Request object back through a channel.
	callbacks := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callbacks <- r
	}))
	server.URL += "/callback"
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(callbacks) })
	return &localCallbackServer{Server: server, t: t, callbacks: callbacks}
}

type localCallbackServer struct {
	*httptest.Server
	t         *testing.T
	callbacks <-chan *http.Request
}

func (s *localCallbackServer) waitForCallback(timeout time.Duration) *http.Request {
	select {
	case callback := <-s.callbacks:
		return callback
	case <-time.After(timeout):
		require.Fail(s.t, "timed out waiting for callback request")
		return nil
	}
}

func doTokenExchange(t *testing.T, config *oauth2.Config, tokenResponse *oauth2.Token, httpClient *http.Client, provider *coreosoidc.Provider) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Form the HTTP POST request with the parameters specified by RFC8693.
	reqBody := strings.NewReader(url.Values{
		"grant_type":           []string{"urn:ietf:params:oauth:grant-type:token-exchange"},
		"audience":             []string{"cluster-1234"},
		"client_id":            []string{config.ClientID},
		"subject_token":        []string{tokenResponse.AccessToken},
		"subject_token_type":   []string{"urn:ietf:params:oauth:token-type:access_token"},
		"requested_token_type": []string{"urn:ietf:params:oauth:token-type:jwt"},
	}.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.Endpoint.TokenURL, reqBody)
	require.NoError(t, err)
	req.Header.Set("content-type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, resp.StatusCode, http.StatusOK)
	defer func() { _ = resp.Body.Close() }()
	var respBody struct {
		AccessToken     string `json:"access_token"`
		IssuedTokenType string `json:"issued_token_type"`
		TokenType       string `json:"token_type"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&respBody))

	var clusterVerifier = provider.Verifier(&coreosoidc.Config{ClientID: "cluster-1234"})
	exchangedToken, err := clusterVerifier.Verify(ctx, respBody.AccessToken)
	require.NoError(t, err)

	var claims map[string]interface{}
	require.NoError(t, exchangedToken.Claims(&claims))
	indentedClaims, err := json.MarshalIndent(claims, "   ", "  ")
	require.NoError(t, err)
	t.Logf("exchanged token claims:\n%s", string(indentedClaims))
}

func expectSecurityHeaders(t *testing.T, response *http.Response, expectFositeToOverrideSome bool) {
	h := response.Header
	assert.Equal(t, "default-src 'none'; frame-ancestors 'none'", h.Get("Content-Security-Policy"))
	assert.Equal(t, "DENY", h.Get("X-Frame-Options"))
	assert.Equal(t, "1; mode=block", h.Get("X-XSS-Protection"))
	assert.Equal(t, "nosniff", h.Get("X-Content-Type-Options"))
	assert.Equal(t, "no-referrer", h.Get("Referrer-Policy"))
	assert.Equal(t, "off", h.Get("X-DNS-Prefetch-Control"))
	if expectFositeToOverrideSome {
		assert.Equal(t, "no-store", h.Get("Cache-Control"))
	} else {
		assert.Equal(t, "no-cache,no-store,max-age=0,must-revalidate", h.Get("Cache-Control"))
	}
	assert.Equal(t, "no-cache", h.Get("Pragma"))
	assert.Equal(t, "0", h.Get("Expires"))
}
