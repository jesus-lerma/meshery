package mesh

import (
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/layer5io/meshery/mesheryctl/internal/cli/root/config"
	"github.com/layer5io/meshery/mesheryctl/pkg/utils"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Operation is the common body type to be passed for Mesh Ops
type Operation struct {
	Adapter    string `json:"adapter"`
	CustomBody string `json:"customBody"`
	DeleteOp   string `json:"deleteOp"`
	Namespace  string `json:"namespace"`
	Query      string `json:"query"`
}

var spec string
var adapterURL string
var namespace string
var tokenPath string
var watch bool
var err error

// validateCmd represents the service mesh validation command
var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate conformance to service mesh standards",
	Args:  cobra.NoArgs,
	Long:  `Validate service mesh conformance to different standard specifications`,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		log.Infof("Verifying prerequisites...")
		mctlCfg, err := config.GetMesheryCtl(viper.GetViper())
		if err != nil {
			log.Fatalln(err)
		}
		// sync with available adapters
		if err = validateAdapter(mctlCfg, tokenPath, meshName); err != nil {
			log.Fatalln(err)
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {

		log.Infof("Starting service mesh validation...")

		mctlCfg, err := config.GetMesheryCtl(viper.GetViper())
		if err != nil {
			log.Fatalln(err)
		}

		_, err = sendValidateRequest(mctlCfg, meshName, false)
		if err != nil {
			log.Fatalln(err)
		}

		if watch {
			log.Infof("Verifying Operation")
			_, err = waitForValidateResponse(mctlCfg, "Smi conformance test")
			if err != nil {
				log.Fatalln(err)
			}
		}

		return nil
	},
}

func init() {
	validateCmd.Flags().StringVarP(&spec, "spec", "s", "smi", "specification to be used for conformance test")
	_ = validateCmd.MarkFlagRequired("spec")
	validateCmd.Flags().StringVarP(&adapterURL, "adapter", "a", "meshery-osm:10010", "Adapter to use for validation")
	_ = validateCmd.MarkFlagRequired("adapter")
	validateCmd.Flags().StringVarP(&tokenPath, "tokenPath", "t", "", "Path to token for authenticating to Meshery API")
	_ = validateCmd.MarkFlagRequired("tokenPath")
	validateCmd.Flags().BoolVarP(&watch, "watch", "w", false, "Watch for events and verify operation (in beta testing)")
}

func waitForValidateResponse(mctlCfg *config.MesheryCtlConfig, query string) (string, error) {
	path := mctlCfg.GetBaseMesheryURL() + "/api/events?client=cli_validate"
	method := "GET"
	client := &http.Client{}
	req, err := http.NewRequest(method, path, nil)
	req.Header.Add("Accept", "text/event-stream")
	if err != nil {
		return "", ErrCreatingDeployResponseRequest(err)
	}

	err = utils.AddAuthDetails(req, tokenPath)
	if err != nil {
		return "", ErrAddingAuthDetails(err)
	}

	res, err := client.Do(req)
	if err != nil {
		return "", ErrCreatingValidateRequest(err)
	}

	event, err := utils.ConvertRespToSSE(res)
	if err != nil {
		return "", ErrCreatingValidateResponseStream(err)
	}

	timer := time.NewTimer(time.Duration(1200) * time.Second)
	eventChan := make(chan string)

	//Run a goroutine to wait for the response
	go func() {
		for i := range event {
			if strings.Contains(i.Data.Summary, query) {
				eventChan <- "successful"
				log.Infof("%s\n%s", i.Data.Summary, i.Data.Details)
			} else if strings.Contains(i.Data.Details, "error") {
				eventChan <- "error"
				log.Infof("%s", i.Data.Summary)
			}
		}
	}()

	select {
	case <-timer.C:
		return "", ErrTimeoutWaitingForValidateResponse
	case event := <-eventChan:
		if event != "successful" {
			return "", ErrSMIConformanceTestsFailed
		}
	}

	return "", nil
}

func sendValidateRequest(mctlCfg *config.MesheryCtlConfig, query string, delete bool) (string, error) {
	path := mctlCfg.GetBaseMesheryURL() + "/api/mesh/ops"
	method := "POST"

	data := url.Values{}
	data.Set("adapter", adapterURL)
	data.Set("query", query)
	data.Set("customBody", "")
	data.Set("namespace", namespace)
	if delete {
		data.Set("deleteOp", "on")
	} else {
		data.Set("deleteOp", "")
	}

	// Choose which specification to use for conformance test
	switch spec {
	case "smi":
		{
			data.Set("query", "smi_conformance")
			break
		}
	case "istio-vet":
		{
			if adapterURL == "meshery-istio:10000" {
				data.Set("query", "istio-vet")
				break
			}
			return "", errors.New("only Istio supports istio-vet operation")
		}
	default:
		{
			return "", errors.New("specified specification not found or not yet supported")
		}
	}

	payload := strings.NewReader(data.Encode())

	client := &http.Client{}
	req, err := http.NewRequest(method, path, payload)
	if err != nil {
		return "", ErrCreatingValidateRequest(err)
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")

	err = utils.AddAuthDetails(req, tokenPath)
	if err != nil {
		return "", ErrAddingAuthDetails(err)
	}

	res, err := client.Do(req)
	if err != nil {
		return "", ErrCreatingDeployRequest(err)
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
