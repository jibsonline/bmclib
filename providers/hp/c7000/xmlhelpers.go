package c7000

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"net/url"

	log "github.com/sirupsen/logrus"
)

// wraps the XML to be sent in the SOAP envelope
func wrapXML(element interface{}, sessionKey string) (doc Envelope) {

	body := Body{Content: element}
	doc = Envelope{
		SOAPENV: "http://www.w3.org/2003/05/soap-envelope",
		Xsi:     "http://www.w3.org/2001/XMLSchema-instance",
		Xsd:     "http://www.w3.org/2001/XMLSchema",
		Wsu:     "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd",
		Wsse:    "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd",
		Hpoa:    "hpoa.xsd",
		Body:    body,
	}

	if sessionKey != "" {

		doc.Header = Header{Security: Security{
			MustUnderstand: "true",
			HpOaSessionKeyToken: HpOaSessionKeyToken{
				OaSessionKey: OaSessionKey{Text: sessionKey},
			},
		},
		}
	}

	return doc
}

func (c *C7000) postXML(data []byte) (statusCode int, body []byte, err error) {
	if err != nil {
		return statusCode, body, err
	}

	u, err := url.Parse(fmt.Sprintf("https://%s/hpoa", c.ip))
	if err != nil {
		return 0, []byte{}, err
	}

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader(data))
	if err != nil {
		return 0, []byte{}, err
	}
	//	req.Header.Add("Content-Type", "application/soap+xml; charset=utf-8")
	req.Header.Add("Content-Type", "text/plain;charset=UTF-8")
	if log.GetLevel() == log.DebugLevel {
		log.Println(fmt.Sprintf("https://%s/hpoa", c.ip))
		dump, err := httputil.DumpRequestOut(req, true)
		if err == nil {
			log.Printf("%s\n\n", dump)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, []byte{}, err
	}
	defer resp.Body.Close()

	if log.GetLevel() == log.DebugLevel {
		dump, err := httputil.DumpResponse(resp, true)
		if err == nil {
			log.Printf("%s\n\n", dump)
		}
	}

	body, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, []byte{}, err
	}

	//fmt.Printf("%+v\n", body)
	return resp.StatusCode, body, err
}
