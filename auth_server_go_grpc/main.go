package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"

	"github.com/envoyproxy/go-control-plane/envoy/api/v2/core"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/auth/v2"
	envoytype "github.com/envoyproxy/go-control-plane/envoy/type"
	"github.com/go-resty/resty/v2"
	"github.com/gogo/googleapis/google/rpc"
	"github.com/gogo/protobuf/types"
	"google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

const (
	port = ":8000"

	AuthCookieName = "id_token"

	extauth_service = "extauth:8080"

	client_id     = "test"
	client_secret = "bc375223-9270-44dc-901f-0bc1450e3a2e"
	keycloak_host = "https://keycloak.sololabs.dev"
	realm_name    = "k8s"
)

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

	// create a dummy request to parse the cookie.
	// unfortunately go's cookie parsing is only available from a request instance.
	if headers != nil {
		if cookieHeader, ok := headers["cookie"]; ok {
			// create a dummy request to parse the cookie.
			// unfortunately go's cookie parsing is only available from a request instance.
			req := http.Request{Header: make(http.Header)}
			req.Header.Add("cookie", cookieHeader)
			if cookie, err := req.Cookie(AuthCookieName); err == nil {
				token := cookie.Value

				// here we have a jwt token that we know it is verified.
				// ZBAM!

				log.Printf("Token, %v", token)

				client := resty.New()

				kcResp, err := client.R().
					SetHeader("Content-Type", "application/x-www-form-urlencoded").
					SetFormData(map[string]string{
						"grant_type":    "client_credentials",
						"client_id":     client_id,
						"client_secret": client_secret,
					}).
					Post(keycloak_host + "/auth/realms/" + realm_name + "/protocol/openid-connect/token")

				if err != nil {
					log.Printf("Denied - Error, %v", err)

					return &pb.CheckResponse{
						Status: &rpc.Status{Code: int32(rpc.PERMISSION_DENIED)},
					}, err
				}

				log.Printf("Keycloak Response Code, %v", kcResp.StatusCode())
				log.Printf("Keycloak Body, %v", string(kcResp.Body()))

				var jsClient struct {
					AccessToken      string `json:"access_token"`
					ExpiresIn        int32  `json:"expires_in"`
					RefreshExpiresIn int32  `json:"refresh_expires_in"`
					RefreshToken     string `json:"refresh_token"`
					TokenType        string `json:"token_type"`
					IdToken          string `json:"id_token"`
					NotBeforePolicy  int32  `json:"not-before-policy"`
					SessionState     string `json:"session_state"`
				}

				_ = json.Unmarshal(kcResp.Body(), &jsClient)

				log.Printf("Path: %v", path)

				kcResp, err = client.R().
					SetHeader("Authorization", "Bearer "+jsClient.AccessToken).
					SetFormData(map[string]string{
						"grant_type":    "urn:ietf:params:oauth:grant-type:uma-ticket",
						"audience":      client_id,
						"permission":    path,
						"response_mode": "decision",
					}).
					Post(keycloak_host + "/auth/realms/" + realm_name + "/protocol/openid-connect/token")

				if err != nil {
					log.Printf("Denied - Error, %v", err)

					return &pb.CheckResponse{
						Status: &rpc.Status{Code: int32(rpc.PERMISSION_DENIED)},
					}, err
				}

				log.Printf("Keycloak Response Code, %v", kcResp.StatusCode())
				log.Printf("Keycloak Body, %v", string(kcResp.Body()))

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

				log.Println("Denied")
				return &pb.CheckResponse{
					Status: &rpc.Status{Code: int32(rpc.PERMISSION_DENIED)},
					HttpResponse: &pb.CheckResponse_DeniedResponse{
						DeniedResponse: &pb.DeniedHttpResponse{
							Status: &envoytype.HttpStatus{Code: envoytype.StatusCode_Forbidden},
							Body:   `{"msg": "denied"}`,
						},
					},
				}, nil
			}
		}
	}

	log.Println("Denied")
	return &pb.CheckResponse{
		Status: &rpc.Status{Code: int32(rpc.PERMISSION_DENIED)},
		HttpResponse: &pb.CheckResponse_DeniedResponse{
			DeniedResponse: &pb.DeniedHttpResponse{
				Status: &envoytype.HttpStatus{Code: envoytype.StatusCode_Forbidden},
				Body:   `{"msg": "denied"}`,
			},
		},
	}, nil
}

func main() {
	lis, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	conn, err := grpc.Dial(extauth_service, grpc.WithInsecure())
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
