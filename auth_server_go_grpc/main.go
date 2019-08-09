package main

import (
	"context"
	"log"
	"net"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/auth/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	googlerpc "github.com/gogo/googleapis/google/rpc"

	gocloak "github.com/Nerzal/gocloak/v3"
)

const (
	port = ":8000"
)

type server struct {
}

func (s *server) Check(ctx context.Context, req *pb.CheckRequest) (*pb.CheckResponse, error) {
	http := req.GetAttributes().GetRequest().GetHttp()
	headers := http.GetHeaders()
	path := http.GetPath()

	log.Printf("Headers %v", headers)
	log.Printf("Path %v", path)

	conn, err := grpc.Dial("extauth:8080", grpc.WithInsecure())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()

	c := pb.NewAuthorizationClient(conn)

	resp, err := c.Check(ctx, req)
	if err != nil {
		log.Fatalf("could not check: %v", err)
	}

	log.Printf("Response Status %v", googlerpc.Code_name[resp.GetStatus().GetCode()])

	if resp.GetStatus().GetCode() == googlerpc.Code_value["OK"] {
		client := gocloak.NewClient("https://localhost:9090")
		_, err := client.LoginClient("test", "6b278493-2769-4d0c-b828-a1991e9dfd4b", "k8s")
		if err != nil {
			panic("Login failed:"+ err.Error())
		}
	}

	return resp, err

	// if strings.HasPrefix(path, "/api/pets/1") {
	// 	log.Println("Approved")
	// 	return &pb.CheckResponse{
	// 		Status: &googlerpc.Status{Code: int32(googlerpc.OK)},
	// 		HttpResponse: &pb.CheckResponse_OkResponse{
	// 			OkResponse: &pb.OkHttpResponse{
	// 				Headers: []*core.HeaderValueOption{
	// 					{
	// 						Append: &types.BoolValue{Value: false},
	// 						Header: &core.HeaderValue{
	// 							Key:   "x-my-header",
	// 							Value: "some value from auth server",
	// 						},
	// 					},
	// 				},
	// 			},
	// 		},
	// 	}, nil
	// }
	//
	// log.Println("Denied")
	// return &pb.CheckResponse{
	// 	Status: &googlerpc.Status{Code: int32(googlerpc.PERMISSION_DENIED)},
	// 	HttpResponse: &pb.CheckResponse_DeniedResponse{
	// 		DeniedResponse: &pb.DeniedHttpResponse{
	// 			Status: &envoytype.HttpStatus{Code: envoytype.StatusCode_Forbidden},
	// 			Body:   `{"msg": "denied"}`,
	// 		},
	// 	},
	// }, nil
}

func main() {
	lis, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()

	pb.RegisterAuthorizationServer(s, &server{})

	// Helps Gloo detect this is a gRPC service
	reflection.Register(s)

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
