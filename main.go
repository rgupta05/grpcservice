package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"grpcservice/database"
	"grpcservice/models"
	"grpcservice/pb/graphql"
	"io"
	"io/ioutil"
	"log"
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
	"gorm.io/datatypes"
)

type JWT map[string]interface{}

type Server struct {
	jwt         *JWT
	oauth2Token *oauth2.Token
}

func main() {

	log.Println(" Starting GRPC service for EOS logs...")
	database.Connection()

	var s Server
	var inlineJson = ""

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
	subscription ($search: String!, $cursor: String, $limit: Int64) {
		searchTransactionsForward(query: $search, limit: $limit, cursor: $cursor,irreversibleOnly:true) {
		  undo
		  cursor
		  isIrreversible
		  irreversibleBlockNum
		  trace {
			block {
			  num
			  id
			  confirmed
			  timestamp
			  previous
			}
			id
			status
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
	search := "receiver:xegkypcmeirg"
	cursor := "Tb9oi-JPt8HkWy0t2LjRJ_e7JZY6DlNsVArhIBtAgoij9CeQ3sz0AjQ="
	vars := toVariable(search, cursor, 0)

	executionClient, err := graphqlClient.Execute(ctx, &graphql.Request{Query: queryTemplate, Variables: vars})
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
		undo := gjson.Get(response.Data, "searchTransactionsForward.undo").Bool()
		block := gjson.Get(response.Data, "searchTransactionsForward.trace.block").Get("num")
		timestamp := gjson.Get(response.Data, "searchTransactionsForward.trace.block").Get("timestamp")
		account := gjson.Get(response.Data, "searchTransactionsForward.trace.matchingActions.0.account").Str
		name := gjson.Get(response.Data, "searchTransactionsForward.trace.matchingActions.0.name").Str
		receiver := gjson.Get(response.Data, "searchTransactionsForward.trace.matchingActions.0.receiver").Str
		primaryJson := gjson.Get(response.Data, "searchTransactionsForward.trace.matchingActions.0").Get("json").Raw
		status := gjson.Get(response.Data, "searchTransactionsForward.trace.status").Str
		len := gjson.Get(response.Data, "searchTransactionsForward.trace.matchingActions.#").Int()
		blockid := gjson.Get(response.Data, "searchTransactionsForward.trace.block").Get("id")
		traceid := gjson.Get(response.Data, "searchTransactionsForward.trace.id").Str
		irreversible := gjson.Get(response.Data, "searchTransactionsForward.isIrreversible").Bool()

		fmt.Println("Len:", len)

		for i := 1; i <= int(len-1); i++ {
			inlineaction := gjson.Get(response.Data, "searchTransactionsForward.trace.matchingActions."+fmt.Sprint(i)).Raw
			if i == 1 {
				inlineJson = "[" + inlineJson + inlineaction
			} else {
				inlineJson = inlineJson + "," + inlineaction
			}
			if i == int(len-1) {
				inlineJson = inlineJson + "]"
			}
		}
		fmt.Println("Cursor:", cursor)
		fmt.Println("trace:", block)
		fmt.Println("account:", account)
		fmt.Println("name:", name)
		fmt.Println("primary json:", primaryJson)
		fmt.Println("UNDO:", undo)
		fmt.Println("STATUS:", status)
		fmt.Println("BLOCK ID:", blockid)
		fmt.Println("TRACE ID:", traceid)
		fmt.Println("IRREVERSIBLE ID:", irreversible)
		//s.storage.StoreCursor(cursor)

		actions := models.Cursor{
			Cursorid:       cursor,
			BlockNum:       block.Int(),
			Timestamp:      timestamp.Time(),
			Account:        account,
			Action:         name,
			Receiver:       receiver,
			Data_json:      datatypes.JSON(primaryJson),
			InlineActions:  datatypes.JSON([]byte(inlineJson)),
			IsIrreversible: irreversible,
			InsertedTime:   time.Now(),
		}

		inlineJson = ""

		fmt.Println(actions.InlineActions)
		// //insert into the database
		// equality := reflect.DeepEqual(lastCursor, actions)
		// fmt.Println("EQUALITY---------------------->", equality)
		// fmt.Println(lastCursor)
		// fmt.Println(actions)

		if undo {
			database.DB.Where("cursorid=?", actions.Cursorid).Delete(&actions)
			fmt.Println("Deleted Record with cursor id: ", actions.Cursorid)
			continue
		}

		// if count == 1 && !reflect.DeepEqual(lastCursor, actions) && lastCursor.Cursorid != "" {
		// 	database.DB.Model(&actions).Where("block_num=?", actions.BlockNum).Updates(map[string]interface{}{"cursorid": actions.Cursorid, "account": actions.Account, "action": actions.Action, "receiver": actions.Receiver, "inline_actions": actions.InlineActions, "data_json": actions.Data_json})
		// 	fmt.Printf("Updated Record with cursor id: %v , Block num: %v", actions.Cursorid, actions.BlockNum)
		// 	continue
		// }
		if actions.BlockNum != 0 && status == "EXECUTED" {
			tx := database.DB.Create(&actions)
			if tx.Error != nil {
				log.Printf("Error for block num: %d %v %v", actions.BlockNum, actions.Cursorid, tx.Error)
				continue
			}
			fmt.Printf("Inserted Record with cursor id: %v , Block num: %v \n", actions.Cursorid, actions.BlockNum)
		}

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

func toVariable(query string, cursor string, limit int64) *structpb.Struct {
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
			"limit": {
				Kind: &structpb.Value_NumberValue{
					NumberValue: float64(limit),
				},
			},
		},
	}

}
