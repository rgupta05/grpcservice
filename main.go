package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"grpcservice/pb/graphql"
	"io"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"golang.org/x/oauth2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"
	"google.golang.org/protobuf/types/known/structpb"
)

type JWT map[string]interface{}

type Server struct {
	jwt         *JWT
	oauth2Token *oauth2.Token
}

func main() {

	var s Server

	authToken, err := s.RefreshToken()
	if err != nil {
		fmt.Errorf("run: %s", err)
	}

	credential := oauth.NewOauthAccess(authToken)
	pool, _ := x509.SystemCertPool()
	// error handling omitted
	creds := credentials.NewClientTLSFromCert(pool, "")
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithPerRPCCredentials(credential),
	}

	connection, err := grpc.Dial("testnet.eos.dfuse.io:443", opts...)
	if err != nil {
		fmt.Errorf("run: grapheos connection connection: %s", err)
	}

	fmt.Println(connection)

	ctx := context.Background()
	graphqlClient := graphql.NewGraphQLClient(connection)

	fmt.Println("Client------------------>", graphqlClient)

	queryTemplate := `
	subscription  {
		searchTransactionsForward(query:"receiver:eosio.token action:transfer -data.quantity:'0.0001 EOS'", limit: 10, cursor: "") {
		  undo
		  cursor
		  trace {
			block {
			  num
			  id
			  confirmed
			  timestamp
			  previous
			}
			id
			matchingActions {
			  account
			  name
			  json
			  seq
			  receiver
			}
		  }
		}
	  }
	  `

	query := "receiver:eosio.token action:transfer -data.quantity:'0.0001 EOS'"
	//cursor := ""
	fmt.Println(query)
	//vars := toVariable(query, cursor, 0)

	executionClient, err := graphqlClient.Execute(ctx, &graphql.Request{Query: queryTemplate})
	if err != nil {
		fmt.Errorf("run: grapheos exec: %s", err)
	}

	for {
		fmt.Println("Waiting for response")
		response, err := executionClient.Recv()

		fmt.Println("Response -----> ", response)

		if err != nil {
			fmt.Println(err)
			if err != io.EOF {
				fmt.Errorf("receiving message from search stream client: %s", err)
			}
			fmt.Println("No more result available")
			break
		}
		fmt.Println("Received response:", response.Data)

		//Handling error from lib subscription

		if len(response.Errors) > 0 {

			for _, e := range response.Errors {
				fmt.Println("Error:", e.Message)
			}

		}

		cursor := gjson.Get(response.Data, "searchTransactionsForward.cursor").Str
		fmt.Println("Cursor:", cursor)
		//	s.storage.StoreCursor(cursor)

	}

}

func (s *Server) RefreshToken() (*oauth2.Token, error) {
	fmt.Println(s.jwt)
	if s.jwt != nil && !s.jwt.NeedRefresh() {
		fmt.Println("Reusing token")
		return s.oauth2Token, nil
	}

	fmt.Println("Getting new token")
	jwt, token, err := s.fetchToken()
	if err != nil {
		return nil, fmt.Errorf("refresh token: %s", err)
	}

	s.jwt = jwt
	s.oauth2Token = &oauth2.Token{
		AccessToken: token,
		TokenType:   "Bearer",
	}

	fmt.Println("token:", token)

	return s.oauth2Token, nil
}

func (s *Server) fetchToken() (*JWT, string, error) {

	jsonData, err := s.postFetchToken()

	if err != nil {
		return nil, "", fmt.Errorf("http fetch: %s", err)
	}

	var resp *struct {
		Token      string `json:"token"`
		Expiration int64  `json:"expires_at"`
	}

	err = json.Unmarshal(jsonData, &resp)

	fmt.Println(resp.Token)

	if err != nil {
		return nil, "", fmt.Errorf("resp unmarshall: %s", err)
	}

	jwt, err := ParseJwt(resp.Token)
	if err != nil {
		return nil, "", fmt.Errorf("jwt parse: %s", err)
	}

	return jwt, resp.Token, nil
}

func (s *Server) postFetchToken() (body []byte, err error) {

	payload := `{"api_key":"server_9f32c26df033e345bed61c12ae641720"}`

	httpResp, err := http.Post("https://auth.dfuse.io/v1/auth/issue", "application/json", bytes.NewBuffer([]byte(payload)))
	if err != nil {
		return nil, fmt.Errorf("request creation: %s", err)
	}
	defer httpResp.Body.Close()

	fmt.Println("fetch token response Status:", httpResp.Status)

	if httpResp.StatusCode != 200 {
		return nil, fmt.Errorf("http status: %s", httpResp.Status)
	}

	data, err := ioutil.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("response read body: %s", err)
	}
	return data, nil
}

func (jwt JWT) NeedRefresh() bool {
	exp := jwt["exp"].(float64)
	iat := jwt["iat"].(float64)

	lifespan := exp - iat
	threshold := float64(lifespan) * 0.05
	fmt.Println("lifespan:", lifespan)
	fmt.Println("refresh threshold:", threshold)

	expireAt := time.Unix(int64(exp), 0)
	now := time.Now()

	timeBeforeExpiration := expireAt.Sub(now)
	if timeBeforeExpiration < 0 {
		return true
	}

	return timeBeforeExpiration.Seconds() < threshold
}

func ParseJwt(token string) (*JWT, error) {
	var re = regexp.MustCompile(`/-/g`)
	var re2 = regexp.MustCompile(`/_/g`)

	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid jwt: missing parts")
	}

	base64Url := parts[1]
	b64 := re.ReplaceAllString(base64Url, "+")
	b64 = re2.ReplaceAllString(b64, "/")

	jwtBytes, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("base 64 decode: %s", err)
	}

	var jwt *JWT
	err = json.Unmarshal(jwtBytes, &jwt)
	if err != nil {
		return nil, fmt.Errorf("jwt unmarshall: %s", err)
	}

	return jwt, nil

}

func toVariable(query string, cursor string, lowBlockNum int32) *structpb.Struct {
	return &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"search": {
				Kind: &structpb.Value_StringValue{
					StringValue: query,
				},
			},
			"cursor": {
				Kind: &structpb.Value_StringValue{
					StringValue: cursor,
				},
			},
			"lowBlockNum": {
				Kind: &structpb.Value_NumberValue{
					NumberValue: float64(lowBlockNum),
				},
			},
		},
	}

}
