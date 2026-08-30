package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Arman2122/p-ui/internal/util/common"
	"github.com/Arman2122/p-ui/internal/web/service"
)

type NordService struct {
	service.SettingService
}

/*
nordAPIBase is the API root, and a variable so a test can point these calls at a
stub: they are otherwise reachable only over the internet.

The client is no longer a package-level one on the default transport. Every
other outbound call the panel makes -- its sibling WARP integration in this same
file's package included -- goes through the operator's configured egress proxy,
and these three quietly did not. On a panel that reaches the internet only
through that proxy, the whole NordVPN integration simply failed. F357.
*/
var nordAPIBase = "https://api.nordvpn.com"

const nordTimeout = 15 * time.Second

// maxResponseSize limits the maximum size of NordVPN API responses (10 MB).
const maxResponseSize = 10 << 20

func (s *NordService) GetCountries() (string, error) {
	req, reqErr := http.NewRequestWithContext(context.Background(), http.MethodGet, nordAPIBase+"/v1/countries", nil)
	if reqErr != nil {
		return "", reqErr
	}
	resp, err := s.NewProxiedHTTPClient(nordTimeout).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", common.NewErrorf("NordVPN API error: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (s *NordService) GetServers(countryId string) (string, error) {
	// Validate countryId is numeric to prevent URL injection
	for _, c := range countryId {
		if c < '0' || c > '9' {
			return "", common.NewError("invalid country ID")
		}
	}
	url := fmt.Sprintf("%s/v2/servers?limit=0&filters[servers_technologies][id]=35&filters[country_id]=%s", nordAPIBase, countryId)
	req, reqErr := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if reqErr != nil {
		return "", reqErr
	}
	resp, err := s.NewProxiedHTTPClient(nordTimeout).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", common.NewErrorf("NordVPN API error: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return "", err
	}
	/*
		Passed through as Nord sent it.

		This used to keep a server only when its load was ABOVE 7 percent, which
		is the filter upside down: the least loaded servers -- the ones worth
		picking -- were the ones dropped, and a country quiet enough that every
		server sat at or below 7 came back empty. A server whose load field was
		missing or not a number was dropped as well.

		Not re-inverted with a threshold of its own: which load is too high is a
		policy nobody chose, and the operator can see the number on each row.
		F356.
	*/
	return string(body), nil
}

func (s *NordService) SetKey(privateKey string) (string, error) {
	if privateKey == "" {
		return "", common.NewError("private key cannot be empty")
	}
	nordData := map[string]string{
		"private_key": privateKey,
		"token":       "",
	}
	data, _ := json.Marshal(nordData)
	err := s.SetNord(string(data))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (s *NordService) GetCredentials(token string) (string, error) {
	url := nordAPIBase + "/v1/users/services/credentials"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth("token", token)

	resp, err := s.NewProxiedHTTPClient(nordTimeout).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", common.NewErrorf("NordVPN API error: %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return "", err
	}

	var creds map[string]any
	if err := json.Unmarshal(body, &creds); err != nil {
		return "", err
	}

	privateKey, ok := creds["nordlynx_private_key"].(string)
	if !ok || privateKey == "" {
		return "", common.NewError("failed to retrieve NordLynx private key")
	}

	nordData := map[string]string{
		"private_key": privateKey,
		"token":       token,
	}
	data, _ := json.Marshal(nordData)
	err = s.SetNord(string(data))
	if err != nil {
		return "", err
	}

	return string(data), nil
}

func (s *NordService) GetNordData() (string, error) {
	return s.GetNord()
}

func (s *NordService) DelNordData() error {
	return s.SetNord("")
}
