package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Nerzal/gocloak/v8"
	_ "github.com/joho/godotenv/autoload"
)

// Global Mautic credentials
var mauticUser = os.Getenv("MAUTIC_USER")
var mauticPW = os.Getenv("MAUTIC_PW")
var mauticURL = os.Getenv("MAUTIC_URL")

func main() {
	http.HandleFunc("/segments", keycloakAuth(getSegmentAndIdInfo))

	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		fmt.Printf(err.Error())
	}

}

type SegmentData struct {
	Total int                `json:"total"`
	Lists map[string]Segment `json:"lists"`
}

type Segment struct {
	IsPublished        bool          `json:"isPublished"`
	DateAdded          time.Time     `json:"dateAdded"`
	DateModified       time.Time     `json:"dateModified"`
	CreatedBy          int           `json:"createdBy"`
	CreatedByUser      string        `json:"createdByUser"`
	ModifiedBy         int           `json:"modifiedBy"`
	ModifiedByUser     string        `json:"modifiedByUser"`
	ID                 int           `json:"id"`
	Name               string        `json:"name"`
	Alias              string        `json:"alias"`
	Description        interface{}   `json:"description"`
	Filters            []interface{} `json:"filters"`
	IsGlobal           bool          `json:"isGlobal"`
	IsPreferenceCenter bool          `json:"isPreferenceCenter"`
}

type SegmentAndID struct {
	IsChecked   bool
	SegmentName string
	SegmentID   string
}

type ContactInfoByEmail struct {
	Total    string                 `json:"total"`
	Contacts map[string]interface{} `json:"contacts"`
}

func getSegmentAndIdInfo(w http.ResponseWriter, r *http.Request) {
	// Get contact's email
	contactEmailHeader := strings.Fields(r.Header.Get("Email"))
	if len(contactEmailHeader) != 1 {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Invalid Email header. Only one email is accepted")
		return
	}
	contactEmail := contactEmailHeader[0]

	// Mautic auth
	client := &http.Client{}
	req, err := http.NewRequest("GET", mauticURL+"api/segments", nil)
	req.SetBasicAuth(mauticUser, mauticPW)
	resp, err := client.Do(req)

	// Get all Segment Names and IDs
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, "Mautic HTTP request failed with error %s\n", err)
	} else {
		bodyText, _ := ioutil.ReadAll(resp.Body)
		dec := json.NewDecoder(strings.NewReader(string(bodyText)))
		segmentAndIDs := []SegmentAndID{}

		for {
			var data SegmentData
			if err := dec.Decode(&data); err == io.EOF {
				break
			} else if err != nil {
				fmt.Fprintf(w, "Decode failed with error %s\n", err)
			}
			// Append segment and ID to output
			for _, value := range data.Lists {
				curSegmentAndID := SegmentAndID{false, value.Name, strconv.Itoa(value.ID)}
				segmentAndIDs = append(segmentAndIDs, curSegmentAndID)
			}

			// Marshall array to json
			b, err := json.Marshal(segmentAndIDs)
			if err != nil {
				fmt.Fprintf(w, "Marshal failed with error %s\n", err)
			}
			fmt.Fprintf(w, "%s \n", b)
		}
	}

	// Get User segments and IDs using user email
	contactID := getContactIdByEmail(w, r, contactEmail)
	fmt.Printf("%s", contactID)
}

func getContactIdByEmail(w http.ResponseWriter, r *http.Request, contactEmail string) string {
	// Mautic auth
	client := &http.Client{}
	req, err := http.NewRequest("GET", mauticURL+"api/contacts?search=email:+"+contactEmail, nil)
	req.SetBasicAuth(mauticUser, mauticPW)
	resp, err := client.Do(req)

	contactId := ""

	// Get contact ID from response
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, "Mautic HTTP request failed with error %s\n", err)
		return contactId
	}

	bodyText, _ := ioutil.ReadAll(resp.Body)
	dec := json.NewDecoder(strings.NewReader(string(bodyText)))
	for {
		var data ContactInfoByEmail
		if err := dec.Decode(&data); err == io.EOF {
			break
		} else if err != nil {
			fmt.Fprintf(w, "Decode failed with error %s\n", err)
		}

		// Get contact ID
		for key := range data.Contacts {
			contactId = key
		}
	}
	return contactId
}

// keycloak authentication function that wraps handlers needing keycloak auth
func keycloakAuth(fn func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		w.Header().Add("Access-Control-Allow-Origin", "*")
		w.Header().Add("Access-Control-Allow-Credentials", "true")
		w.Header().Add("Access-Control-Allow-Headers", "Content-Type, Authorization, Email")
		w.Header().Add("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")

		if r.Method == "OPTIONS" {
			http.Error(w, "No Content", http.StatusNoContent)
			return
		}
		authHeader := strings.Fields(r.Header.Get("Authorization"))

		if len(authHeader) < 2 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "Invalid authorization header")
			return
		}
		token := authHeader[1]
		kcClientID := os.Getenv("KC_CLIENT_ID")
		kcClientSecret := os.Getenv("KC_CLIENT_SECRET")
		kcRealm := os.Getenv("KC_REALM")
		kcURL := os.Getenv("KC_URL")
		kcClient := gocloak.NewClient((kcURL))

		ctx := context.Background()
		_, err := kcClient.LoginClient(ctx, kcClientID, kcClientSecret, kcRealm)

		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, "Keycloak Login failed:"+err.Error())
			return
		}

		rptResult, err := kcClient.RetrospectToken(ctx, token, kcClientID, kcClientSecret, kcRealm)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, "Keycloak inspection failed:"+err.Error())
			return
		}

		if !*rptResult.Active {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, "Keycloak token is not active")
			return
		}

		//Execute handler function if token is valid
		fn(w, r)
	}
}
