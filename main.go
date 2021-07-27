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
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/tidwall/gjson"
	"golang.org/x/oauth2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"
	"google.golang.org/protobuf/types/known/structpb"
	"gorm.io/datatypes"
)

type JWT map[string]interface{}

var s Server

type Server struct {
	jwt         *JWT
	oauth2Token *oauth2.Token
}

func main() {

	database.Connection()

	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/continue-stream", index).Methods("GET")

	headersOk := handlers.AllowedHeaders([]string{"X-Requested-With", "Content-Type", "application/json;charset=UTF-8"})
	originsOk := handlers.AllowedOrigins([]string{"*"})
	methodsOk := handlers.AllowedMethods([]string{"GET", "HEAD", "POST", "PUT", "OPTIONS"})

	fmt.Println("Listening on :80...")
	srv := &http.Server{
		Handler:      handlers.CORS(originsOk, headersOk, methodsOk)(router),
		Addr:         ":80",
		WriteTimeout: 300 * time.Second,
		ReadTimeout:  300 * time.Second,
	}

	err := srv.ListenAndServe()
	if err != nil {
		fmt.Println("Error launching server: ", err)
	}
}

func index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	resp := models.Response{
		Code:    200,
		Message: "Stream Started",
	}
	json.NewEncoder(w).Encode(resp)
	go StreamData()
}

func StreamData() {

	var inlineJson = ""
	var queryTemplate string
	var lastCursor models.Cursor
	var count = 0

	authToken, err := RefreshToken()
	if err != nil {
		errResult := fmt.Errorf("run: %s", err)
		log.Println(errResult)
	}

	s.oauth2Token = &oauth2.Token{
		AccessToken: authToken.AccessToken,
		TokenType:   "Bearer",
	}

	credential := oauth.NewOauthAccess(s.oauth2Token)
	pool, _ := x509.SystemCertPool()
	// error handling omitted
	creds := credentials.NewClientTLSFromCert(pool, "")
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithPerRPCCredentials(credential),
	}

	connection, err := grpc.Dial("eos.dfuse.eosnation.io:9000", opts...)
	if err != nil {
		errResult := fmt.Errorf("run: grapheos connection connection: %s", err)
		log.Println(errResult)
	}

	fmt.Println("Connection:", connection)

	ctx := context.Background()
	graphqlClient := graphql.NewGraphQLClient(connection)

	fmt.Println("Client------------------>", graphqlClient)

	queryTemplate = `
	subscription ($search: String!, $cursor: String,$lowBlockNum: Int64) {
		searchTransactionsForward(query: $search, lowBlockNum: $lowBlockNum, limit: 0, cursor: $cursor) {
		  undo
		  cursor
		  trace {
			block {
			  num
			  timestamp
			}
			matchingActions {
			  account
			  name
			  json
			  receiver
			}
		  }
		}
	  } 
	  
	  `

	database.DB.Select("cursorid", "block_num", "account", "action", "receiver", "inline_actions", "data_json", "timestamp").Last(&lastCursor)
	lowBlockNum := fmt.Sprint(lastCursor.BlockNum)
	fmt.Println("LAST INSERTED BLOCK NUM ::::::::", lowBlockNum)

	search := "receiver:hodldexeos11 -action:orasetrate"
	cursor := ""
	fmt.Println(search)
	low, _ := strconv.Atoi(lowBlockNum)
	//limit, _ := strconv.Atoi(limitBlock)
	vars := toVariable(search, cursor, int64(low), 0)

	executionClient, err := graphqlClient.Execute(ctx, &graphql.Request{Query: queryTemplate, Variables: vars})

	fmt.Println("Execution Client------------------>", executionClient)

	if err != nil {
		errResult := fmt.Errorf("run: grapheos exec: %s", err)
		log.Println(errResult)
	} else if executionClient == nil {
		log.Println("Erorr in getting execution client")
		return
	}

	defer StreamData()

	for {
		fmt.Println("Waiting for response")
		count += 1
		response, err := executionClient.Recv()

		fmt.Println("Response -----> ", response)

		//err = errors.New("rpc error: code = Unavailable desc = error reading from server: read tcp 192.168.0.3:61930->148.59.149.144:9000: wsarecv: A connection attempt failed because the connected party did not properly respond after a period of time, or established connection failed because connected host has failed to respond.")

		if err != nil {
			fmt.Println(err)
			if err != io.EOF {
				errResult := fmt.Errorf("receiving message from search stream client: %s", err)
				log.Println(errResult)
				break
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
		block := gjson.Get(response.Data, "searchTransactionsForward.trace.block").Get("num")
		timestamp := gjson.Get(response.Data, "searchTransactionsForward.trace.block").Get("timestamp")
		account := gjson.Get(response.Data, "searchTransactionsForward.trace.matchingActions.0.account").Str
		name := gjson.Get(response.Data, "searchTransactionsForward.trace.matchingActions.0.name").Str
		receiver := gjson.Get(response.Data, "searchTransactionsForward.trace.matchingActions.0.receiver").Str
		primaryJson := gjson.Get(response.Data, "searchTransactionsForward.trace.matchingActions.0").Get("json").Raw
		len := gjson.Get(response.Data, "searchTransactionsForward.trace.matchingActions.#").Int()

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
		//	s.storage.StoreCursor(cursor)

		actions := models.Cursor{
			Cursorid:      cursor,
			BlockNum:      block.Int(),
			Timestamp:     timestamp.Time(),
			Account:       account,
			Action:        name,
			Receiver:      receiver,
			Data_json:     datatypes.JSON(primaryJson),
			InlineActions: datatypes.JSON([]byte(inlineJson)),
		}

		inlineJson = ""

		fmt.Println(actions.InlineActions)
		//insert into the database
		equality := reflect.DeepEqual(lastCursor, actions)
		fmt.Println("EQUALITY---------------------->", equality)
		fmt.Println(lastCursor)
		fmt.Println(actions)
		if count == 1 && !reflect.DeepEqual(lastCursor, actions) && lastCursor.Cursorid != "" {
			database.DB.Model(&actions).Where("block_num=?", actions.BlockNum).Updates(map[string]interface{}{"account": actions.Account, "action": actions.Action, "receiver": actions.Receiver, "inline_actions": actions.InlineActions, "data_json": actions.Data_json})
			continue
		}
		tx := database.DB.Create(&actions)
		if tx.Error != nil {
			count -= 1
			log.Printf("Error for block num: %d %v %v", actions.BlockNum, actions.Cursorid, tx.Error)
		}

	}

}

func RefreshToken() (*oauth2.Token, error) {

	if s.jwt != nil && !s.jwt.NeedRefresh() {
		fmt.Println("Reusing token")
		return s.oauth2Token, nil
	}

	fmt.Println("Getting new token")
	jwt, token, err := fetchToken()
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

func fetchToken() (*JWT, string, error) {

	jsonData, err := postFetchToken()

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

func postFetchToken() (body []byte, err error) {

	payload := `{"api_key":"f25867d1c14a5ca649dcbbd4ad12cc2f"}`

	httpResp, err := http.Post("https://auth.eosnation.io/v1/auth/issue", "application/json", bytes.NewBuffer([]byte(payload)))
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

func toVariable(query string, cursor string, lowBlockNum int64, limit int64) *structpb.Struct {
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
			"limit": {
				Kind: &structpb.Value_NumberValue{
					NumberValue: float64(limit),
				},
			},
		},
	}

}
