package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/envoyproxy/go-control-plane/envoy/api/v2/core"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/auth/v2"
	"github.com/go-resty/resty/v2"
	"github.com/gogo/googleapis/google/rpc"
	"github.com/gogo/protobuf/types"
	"google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

const (
	defaultPort           = "8000"
	defaultExtauthAddress = "extauth:8080"

	AuthCookieName = "id_token"
)

var keycloakClientId string
var keycloakClientSecret string
var keycloakBaseURL string
var keycloakRealm string

type server struct {
	authClient pb.AuthorizationClient
}

func (s *server) Check(ctx context.Context, req *pb.CheckRequest) (*pb.CheckResponse, error) {
	httpReq := req.GetAttributes().GetRequest().GetHttp()
	headers := httpReq.GetHeaders()
	path := httpReq.GetPath()

	log.Printf("Headers %v", headers)
	log.Printf("Path %v", path)

	resp, err := s.authClient.Check(ctx, req)
	if err != nil {
		return resp, err
	}

	log.Printf("Response Status %v", code.Code_name[resp.GetStatus().GetCode()])

	if resp.GetStatus().GetCode() != int32(code.Code_OK) {
		return resp, err
	}

	if headers != nil {
		if cookieHeader, ok := headers["cookie"]; ok {
			// create a dummy request to parse the cookie
			// unfortunately go's cookie parsing is only available from a request instance
			req := http.Request{Header: make(http.Header)}
			req.Header.Add("cookie", cookieHeader)
			if cookie, err := req.Cookie(AuthCookieName); err == nil {
				// here we have a jwt idToken that we know is verified
				idToken := cookie.Value

				log.Printf("ID Token, %v", idToken)

				//
				// Use REST to call Keycloak Authorization APIs as can't find current golang keycloak libraries
				//

				client := resty.New()

				log.Printf("Keycloak Base URL %v", keycloakBaseURL)
				log.Printf("Keycloak Client Credentials %v / %v", keycloakClientId, keycloakClientSecret)

				// Get Access Token needed to make authorization checks
				// Enhancement request for Gloo captures and provides user access_token as part of OpenID Connect
				// authentication to eliminate need for extra credential call
				kcResp, err := client.R().
					SetHeader("Content-Type", "application/x-www-form-urlencoded").
					SetFormData(map[string]string{
						"grant_type":    "client_credentials",
						"client_id":     keycloakClientId,
						"client_secret": keycloakClientSecret,
					}).
					Post(keycloakBaseURL + "/auth/realms/" + keycloakRealm + "/protocol/openid-connect/token")

				if err != nil {
					log.Printf("Denied - Error, %v", err)

					return &pb.CheckResponse{
						Status: &rpc.Status{Code: int32(rpc.PERMISSION_DENIED)},
					}, err
				}

				log.Printf("Keycloak Credential Response Code, %v", kcResp.StatusCode())
				log.Printf("Keycloak Credential Body, %v", string(kcResp.Body()))

				var jsClient struct {
					AccessToken      string `json:"access_token"`
					ExpiresIn        int32  `json:"expires_in"`
					RefreshExpiresIn int32  `json:"refresh_expires_in"`
					RefreshToken     string `json:"refresh_token"`
					TokenType        string `json:"token_type"`
					IDToken          string `json:"id_token"`
					NotBeforePolicy  int32  `json:"not-before-policy"`
					SessionState     string `json:"session_state"`
				}

				_ = json.Unmarshal(kcResp.Body(), &jsClient)

				log.Printf("Path: %v", path)

				// Check permissions assuming request path is a managed resource object in keycloak
				kcResp, err = client.R().
					SetHeader("Authorization", "Bearer "+jsClient.AccessToken).
					SetFormData(map[string]string{
						"grant_type":    "urn:ietf:params:oauth:grant-type:uma-ticket",
						"audience":      keycloakClientId,
						"permission":    path,
						"response_mode": "decision",
					}).
					Post(keycloakBaseURL + "/auth/realms/" + keycloakRealm + "/protocol/openid-connect/token")

				if err != nil {
					log.Printf("Denied - Error, %v", err)

					return &pb.CheckResponse{
						Status: &rpc.Status{Code: int32(rpc.PERMISSION_DENIED)},
					}, err
				}

				log.Printf("Keycloak Permission Response Code, %v", kcResp.StatusCode())
				log.Printf("Keycloak Permission Body, %v", string(kcResp.Body()))

				var jsResult struct {
					Result bool `json:"result"`
				}

				_ = json.Unmarshal(kcResp.Body(), &jsResult)

				if jsResult.Result {
					log.Println("Approved")
					return &pb.CheckResponse{
						Status: &rpc.Status{Code: int32(rpc.OK)},
						HttpResponse: &pb.CheckResponse_OkResponse{
							OkResponse: &pb.OkHttpResponse{
								// Add optional additional response headers hear for upstream usage
								Headers: []*core.HeaderValueOption{
									{
										Append: &types.BoolValue{Value: false},
										Header: &core.HeaderValue{
											Key:   "x-my-header",
											Value: "some value from auth server",
										},
									},
								},
							},
						},
					}, nil
				}

				// fall thru for Denied case
			}
		}
	}

	log.Println("Denied")
	return &pb.CheckResponse{
		Status: &rpc.Status{Code: int32(rpc.PERMISSION_DENIED)},
		HttpResponse: &pb.CheckResponse_DeniedResponse{
			DeniedResponse: &pb.DeniedHttpResponse{
				// Set optional custom body here to return to user for denied requests
				Body: `{"msg": "denied"}`,
			},
		},
	}, nil
}

func main() {
	lis, err := net.Listen("tcp", ":"+getenv("PORT", defaultPort))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	// Pull Keycloak settings from k8s deployment.yaml manifest
	keycloakClientId = mustGetenv("KEYCLOAK_CLIENT_ID")
	keycloakClientSecret = mustGetenv("KEYCLOAK_CLIENT_SECRET")
	keycloakBaseURL = mustGetenv("KEYCLOAK_BASE_URL")
	keycloakRealm = mustGetenv("KEYCLOAK_REALM")

	conn, err := grpc.Dial(getenv("EXTAUTH_ADDRESS", defaultExtauthAddress), grpc.WithInsecure())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()

	client := pb.NewAuthorizationClient(conn)

	s := grpc.NewServer()

	pb.RegisterAuthorizationServer(s, &server{
		authClient: client,
	})

	// Helps Gloo detect this is a gRPC service
	reflection.Register(s)

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if len(value) == 0 {
		return fallback
	}
	return value
}

func mustGetenv(key string) string {
	value := os.Getenv(key)
	if len(value) == 0 {
		log.Fatalf("failed to get required environment varialble %v", key)
	}
	return value
}
