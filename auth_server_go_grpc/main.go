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
	port            = ":8000"
	AuthCookieName  = "id_token"
	extauth_service = "extauth:8080"
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

	// conn, err := grpc.Dial("extauth:8080", grpc.WithInsecure())
	// if err != nil {
	// 	log.Fatalf("did not connect: %v", err)
	// }
	// defer conn.Close()
	//
	// c := pb.NewAuthorizationClient(conn)

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
				return s.authToken(ctx, token)
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

func (s *server) authToken(ctx context.Context, token string) (*pb.CheckResponse, error) {
	// here we have a jwt token that we know it is verified.
	// ZBAM!

	var approved = true

	log.Printf("Token, %v", token)

	client_id := "test"
	client_secret := "bc375223-9270-44dc-901f-0bc1450e3a2e"
	keycloak_host := "https://keycloak.sololabs.dev"
	realm_name := "k8s"

	client := resty.New()

	resp, err := client.R().
		SetHeader("Content-Type", "application/x-www-form-urlencoded").
		SetFormData(map[string]string{
			"grant_type":    "client_credentials",
			"client_id":     client_id,
			"client_secret": client_secret,
		}).
		Post(keycloak_host + "/auth/realms/" + realm_name + "/protocol/openid-connect/token")

	log.Printf("Keycloak Response Code, %v", resp.StatusCode())
	log.Printf("Keycloak Body, %v", string(resp.Body()))

	log.Printf("Keycloak Error, %v", err)

	var foo struct {
		AccessToken string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}

	json.Unmarshal(resp.Body(), &foo)

	log.Printf("Keycloak Access Token, %v", foo.AccessToken)
	log.Printf("Keycloak Refresh Token, %v", foo.RefreshToken)

	// keycloakClient := gocloak.NewClient("https://keycloak.sololabs.dev")
	// _, err := keycloakClient.LoginClient("test", "bc375223-9270-44dc-901f-0bc1450e3a2e", "k8s")
	// if err != nil {
	// 	log.Fatalf("Error connecting to keycloak, %v", err)
	// 	// return nil, err
	// }

	if approved {
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
