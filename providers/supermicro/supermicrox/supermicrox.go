package supermicrox

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"github.com/bmc-toolbox/bmclib/devices"
	"github.com/bmc-toolbox/bmclib/errors"
	"github.com/bmc-toolbox/bmclib/internal/httpclient"
	"github.com/go-logr/logr"

	"github.com/bmc-toolbox/bmclib/providers/supermicro"
)

const (
	// BmcType defines the bmc model that is supported by this package
	BmcType = "supermicrox"

	// X10 is the constant for x10 servers
	X10 = "x10"
	// X11 is the constant for x11 servers
	X11 = "x11"
)

// SupermicroX holds the status and properties of a connection to a supermicro bmc
type SupermicroX struct {
	ip                   string
	username             string
	password             string
	httpClient           *http.Client
	ctx                  context.Context
	log                  logr.Logger
	httpClientSetupFuncs []func(*http.Client)
}

type ChassisInfo struct {
	SerialNumber string `json:"SerialNumber"`
	Error        struct {
		Code            string `json:"code"`
		Message         string `json:"message"`
		ExtendedMessage []*struct {
			MessageId string `json:"MessageId"`
		} `json:"@Message.ExtendedInfo"`
	} `json:"error"`
}

// SupermicroXOption is a type that can configure a *SupermicroX
type SupermicroXOption func(*SupermicroX)

// WithSecureTLS enforces trusted TLS connections, with an optional CA certificate pool.
// Using this option with an nil pool uses the system CAs.
func WithSecureTLS(rootCAs *x509.CertPool) SupermicroXOption {
	return func(i *SupermicroX) {
		i.httpClientSetupFuncs = append(i.httpClientSetupFuncs, httpclient.SecureTLSOption(rootCAs))
	}
}

// New returns a new SupermicroX instance ready to be used
func New(ctx context.Context, ip string, username string, password string, log logr.Logger) (sm *SupermicroX, err error) {
	return NewWithOptions(ctx, ip, username, password, log)
}

// NewWithOptions returns a new SupermicroX with options ready to be used
func NewWithOptions(ctx context.Context, ip string, username string, password string, log logr.Logger, opts ...SupermicroXOption) (*SupermicroX, error) {
	sm := &SupermicroX{
		ip:       ip,
		username: username,
		password: password,
		ctx:      ctx,
		log:      log,
	}
	for _, opt := range opts {
		opt(sm)
	}
	return sm, nil
}

// CheckCredentials verify whether the credentials are valid or not
func (s *SupermicroX) CheckCredentials() (err error) {
	err = s.httpLogin()
	if err != nil {
		return err
	}
	return err
}

// get calls a given json endpoint of the ilo and returns the data
func (s *SupermicroX) get(endpoint string, authentication bool) (payload []byte, err error) {
	err = s.httpLogin()
	if err != nil {
		return nil, err
	}

	bmcURL := fmt.Sprintf("https://%s", s.ip)
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/%s", bmcURL, endpoint), nil)
	if err != nil {
		return nil, err
	}

	u, err := url.Parse(bmcURL)
	if err != nil {
		return nil, err
	}

	for _, cookie := range s.httpClient.Jar.Cookies(u) {
		if cookie.Name == "SID" && cookie.Value != "" {
			req.AddCookie(cookie)
		}
	}

	if authentication {
		req.SetBasicAuth(s.username, s.password)
	}

	reqDump, _ := httputil.DumpRequestOut(req, true)
	s.log.V(2).Info("", "request", fmt.Sprintf("https://%s/%s", bmcURL, endpoint), "requestDump", string(reqDump))

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respDump, _ := httputil.DumpResponse(resp, true)
	s.log.V(2).Info("", "responseDump", string(respDump))

	payload, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == 404 {
		return nil, errors.ErrPageNotFound
	}

	return payload, nil
}

// posts a urlencoded form to the given endpoint
// nolint: gocyclo
func (s *SupermicroX) post(endpoint string, urlValues *url.Values, form []byte, formDataContentType string) (statusCode int, err error) {
	err = s.httpLogin()
	if err != nil {
		return statusCode, err
	}

	u, err := url.Parse(fmt.Sprintf("https://%s/cgi/%s", s.ip, endpoint))
	if err != nil {
		return statusCode, err
	}

	var req *http.Request

	if formDataContentType == "" {
		req, err = http.NewRequest("POST", u.String(), strings.NewReader(urlValues.Encode()))
		if err != nil {
			return statusCode, err
		}
		req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req, err = http.NewRequest("POST", u.String(), bytes.NewReader(form))
		if err != nil {
			return statusCode, err
		}
		// Set multipart form content type
		req.Header.Set("Content-Type", formDataContentType)
	}

	for _, cookie := range s.httpClient.Jar.Cookies(u) {
		if cookie.Name == "SID" && cookie.Value != "" {
			req.AddCookie(cookie)
		}
	}

	reqDump, _ := httputil.DumpRequestOut(req, true)
	s.log.V(2).Info("", "url", fmt.Sprintf("https://%s/cgi/%s", s.ip, endpoint), "requestDump", string(reqDump))

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return statusCode, err
	}
	defer resp.Body.Close()

	respDump, _ := httputil.DumpResponse(resp, true)
	s.log.V(2).Info("", "responseDump", string(respDump))

	statusCode = resp.StatusCode
	_, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return statusCode, err
	}
	return statusCode, err
}

func (s *SupermicroX) query(requestType string) (ipmi *supermicro.IPMI, err error) {
	err = s.httpLogin()
	if err != nil {
		return ipmi, err
	}

	bmcURL := fmt.Sprintf("https://%s/cgi/ipmi.cgi", s.ip)
	s.log.V(1).Info("retrieving data from bmc", "step", "bmc connection", "vendor", string(supermicro.VendorID), "ip", s.ip)

	req, err := http.NewRequest("POST", bmcURL, bytes.NewBufferString(requestType))
	if err != nil {
		return ipmi, err
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	u, err := url.Parse(bmcURL)
	if err != nil {
		return ipmi, err
	}
	for _, cookie := range s.httpClient.Jar.Cookies(u) {
		if cookie.Name == "SID" && cookie.Value != "" {
			req.AddCookie(cookie)
		}
	}
	reqDump, _ := httputil.DumpRequestOut(req, true)
	s.log.V(2).Info("trace", "url", fmt.Sprintf("https://%s/cgi/%s", bmcURL, s.ip), "requestDump", string(reqDump))

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return ipmi, err
	}
	defer resp.Body.Close()

	payload, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return ipmi, err
	}

	respDump, _ := httputil.DumpResponse(resp, true)
	s.log.V(2).Info("", "responseDump", string(respDump))

	ipmi = &supermicro.IPMI{}
	err = xml.Unmarshal(payload, ipmi)
	if err != nil {
		return ipmi, err
	}

	return ipmi, err
}

// Serial returns the device serial
func (s *SupermicroX) Serial() (serial string, err error) {
	ipmi, err := s.query("FRU_INFO.XML=(0,0)")
	if err != nil {
		return "", err
	}

	if ipmi.FruInfo == nil || ipmi.FruInfo.Board == nil {
		return "", errors.ErrInvalidSerial
	}

	return strings.ToLower(ipmi.FruInfo.Board.SerialNum), nil
}

// ChassisSerial returns the serial number of the chassis where the blade is attached
func (s *SupermicroX) ChassisSerial() (serial string, err error) {
	chassisInfo := &ChassisInfo{}
	payload, err := s.get("redfish/v1/Chassis/1", true)
	if err != nil {
		return "", err
	}

	err = json.Unmarshal(payload, chassisInfo)
	if err != nil {
		return "", err
	}

	if chassisInfo.Error.Code != "" {
		e := "Code: " + chassisInfo.Error.Code + ", Message: " + chassisInfo.Error.Message
		for i, s := range chassisInfo.Error.ExtendedMessage {
			e += fmt.Sprintf(", Extended[%d]: %s", i, s)
		}
		return "", fmt.Errorf(e)
	}

	return strings.ToLower(chassisInfo.SerialNumber), nil
}

// HardwareType returns just Model id string - supermicrox
// TODO(ncode): Juliano of the future, please refactor everything related to HardwareType,
//              so that we don't silently swallow errors like you just for this commit
func (s *SupermicroX) HardwareType() (model string) {
	m, err := s.Model()
	if err != nil {
		s.log.V(1).Error(err, "HardwareType(): Getting the hardware type failed.")
		return ""
	}

	return m
}

// Model returns the device model
func (s *SupermicroX) Model() (model string, err error) {
	ipmi, err := s.query("FRU_INFO.XML=(0,0)")
	if err != nil {
		return model, err
	}

	if ipmi.FruInfo != nil && ipmi.FruInfo.Board != nil {
		return ipmi.FruInfo.Board.PartNum, nil
	}

	return "", fmt.Errorf("SupermicroX Model(): Model not found!")
}

// Version returns the version of the bmc we are running
func (s *SupermicroX) Version() (bmcVersion string, err error) {
	ipmi, err := s.query("GENERIC_INFO.XML=(0,0)")
	if err != nil {
		return bmcVersion, err
	}

	if ipmi.GenericInfo != nil {
		if ipmi.GenericInfo.IpmiFwVersion != "" {
			return ipmi.GenericInfo.IpmiFwVersion, err
		} else if ipmi.GenericInfo.Generic != nil {
			return ipmi.GenericInfo.Generic.IpmiFwVersion, err
		}
	}

	return bmcVersion, err
}

// Name returns the hostname of the machine
func (s *SupermicroX) Name() (name string, err error) {
	ipmi, err := s.query("CONFIG_INFO.XML=(0,0)")
	if err != nil {
		return name, err
	}

	if ipmi.ConfigInfo != nil && ipmi.ConfigInfo.Hostname != nil {
		return ipmi.ConfigInfo.Hostname.Name, err
	}

	return name, err
}

// Status returns health string status from the bmc
func (s *SupermicroX) Status() (health string, err error) {
	ipmi, err := s.query("SENSOR_INFO_FOR_SYS_HEALTH.XML=(1,ff)")
	if err != nil {
		return health, err
	}

	if ipmi.HealthInfo != nil && ipmi.HealthInfo.Health == "1" {
		return "OK", err
	}

	return "Unhealthy", err
}

// Memory returns the total amount of memory of the server
func (s *SupermicroX) Memory() (mem int, err error) {
	ipmi, err := s.query("SMBIOS_INFO.XML=(0,0)")

	for _, dimm := range ipmi.Dimm {
		dimm := strings.TrimSuffix(dimm.Size, " MB")
		size, err := strconv.Atoi(dimm)
		if err != nil {
			return mem, err
		}
		mem += size
	}

	return mem / 1024, err
}

// CPU returns the cpu, cores and hyperthreads of the server
func (s *SupermicroX) CPU() (cpu string, cpuCount int, coreCount int, hyperthreadCount int, err error) {
	ipmi, err := s.query("SMBIOS_INFO.XML=(0,0)")
	if err != nil {
		return "", 0, 0, 0, err
	}

	if len(ipmi.CPU) == 0 {
		return "", 0, 0, 0, nil
	}

	entry := ipmi.CPU[0]
	cpu = httpclient.StandardizeProcessorName(entry.Version)
	cpuCount = len(ipmi.CPU)

	coreCount, err = strconv.Atoi(entry.Core)
	if err != nil {
		return cpu, cpuCount, 0, 0, err
	}

	hyperthreadCount = coreCount
	return cpu, cpuCount, coreCount, hyperthreadCount, nil
}

// BiosVersion returns the current version of the bios
func (s *SupermicroX) BiosVersion() (version string, err error) {
	ipmi, err := s.query("SMBIOS_INFO.XML=(0,0)")
	if err != nil {
		return version, err
	}

	if ipmi.Bios != nil {
		return ipmi.Bios.Version, err
	}

	return version, err
}

// PowerKw returns the current power usage in Kw
func (s *SupermicroX) PowerKw() (power float64, err error) {
	ipmi, err := s.query("Get_NodeInfoReadings.XML=(0,0)")
	if err != nil {
		return power, err
	}

	if ipmi.NodeInfo != nil {
		serial, err := s.Serial()
		if err != nil {
			return power, err
		}
		for _, node := range ipmi.NodeInfo.Nodes {
			if strings.ToLower(node.NodeSerial) == serial {
				value, err := strconv.Atoi(node.Power)
				if err != nil {
					return power, err
				}

				return float64(value) / 1000.00, err
			}
		}
	}

	return power, err
}

// PowerState returns the current power state of the machine
func (s *SupermicroX) PowerState() (state string, err error) {
	ipmi, err := s.query("POWER_INFO.XML=(0,0)")
	if err != nil {
		return state, err
	}

	if ipmi.PowerInfo != nil {
		return strings.ToLower(ipmi.PowerInfo.Power.Status), err
	}

	return "unknow", err
}

// TempC returns the current temperature of the machine
func (s *SupermicroX) TempC() (temp int, err error) {
	ipmi, err := s.query("Get_NodeInfoReadings.XML=(0,0)")
	if err != nil {
		return temp, err
	}

	if ipmi.NodeInfo != nil {
		serial, err := s.Serial()
		if err != nil {
			return temp, err
		}
		for _, node := range ipmi.NodeInfo.Nodes {
			if strings.ToLower(node.NodeSerial) == serial {
				temp, err := strconv.Atoi(node.SystemTemp)
				if err != nil {
					return temp, err
				}

				return temp, err
			}
		}
	}

	return temp, err
}

// IsBlade returns if the current hardware is a blade or not
func (s *SupermicroX) IsBlade() (isBlade bool, err error) {
	ipmi, err := s.query("Get_NodeInfoReadings.XML=(0,0)")
	if err != nil {
		return isBlade, err
	}

	if ipmi.NodeInfo != nil {
		for _, node := range ipmi.NodeInfo.Nodes {
			if node.NodeSerial != "" {
				return true, err
			}
		}
	}

	return isBlade, err
}

// Slot returns the current slot within the chassis
func (s *SupermicroX) Slot() (slot int, err error) {
	slot = 1
	ipmi, err := s.query("Get_NodeInfoReadings.XML=(0,0)")
	if err != nil {
		return slot, err
	}

	if ipmi.NodeInfo == nil {
		return slot, errors.ErrUnableToReadData
	}
	serial, err := s.Serial()
	if err != nil {
		return slot, err
	}
	for _, node := range ipmi.NodeInfo.Nodes {
		if strings.ToLower(node.NodeSerial) == serial {
			slot = node.ID + 1
		}
	}

	return slot, err
}

// Nics returns all found Nics in the device
func (s *SupermicroX) Nics() (nics []*devices.Nic, err error) {
	ipmi, err := s.query("GENERIC_INFO.XML=(0,0)")
	if err != nil {
		return nics, err
	}

	if ipmi != nil && ipmi.GenericInfo != nil {
		if ipmi.GenericInfo.BmcMac != "" {
			bmcNic := &devices.Nic{
				Name:       "bmc",
				MacAddress: ipmi.GenericInfo.BmcMac,
			}
			nics = append(nics, bmcNic)
		} else if ipmi.GenericInfo.Generic != nil {
			bmcNic := &devices.Nic{
				Name:       "bmc",
				MacAddress: ipmi.GenericInfo.Generic.BmcMac,
			}
			nics = append(nics, bmcNic)
		}
	}

	ipmi, err = s.query("Get_PlatformInfo.XML=(0,0)")
	if err != nil {
		return nics, err
	}

	// TODO: (ncode) This needs to become dynamic somehow
	if ipmi.PlatformInfo != nil {
		if ipmi.PlatformInfo.MbMacAddr1 != "" {
			bmcNic := &devices.Nic{
				Name:       "eth0",
				MacAddress: ipmi.PlatformInfo.MbMacAddr1,
			}
			nics = append(nics, bmcNic)
		}

		if ipmi.PlatformInfo.MbMacAddr2 != "" {
			bmcNic := &devices.Nic{
				Name:       "eth1",
				MacAddress: ipmi.PlatformInfo.MbMacAddr2,
			}
			nics = append(nics, bmcNic)
		}

		if ipmi.PlatformInfo.MbMacAddr3 != "" {
			bmcNic := &devices.Nic{
				Name:       "eth2",
				MacAddress: ipmi.PlatformInfo.MbMacAddr3,
			}
			nics = append(nics, bmcNic)
		}

		if ipmi.PlatformInfo.MbMacAddr4 != "" {
			bmcNic := &devices.Nic{
				Name:       "eth3",
				MacAddress: ipmi.PlatformInfo.MbMacAddr4,
			}
			nics = append(nics, bmcNic)
		}
	}

	return nics, err
}

// License returns the iLO's license information
func (s *SupermicroX) License() (name string, licType string, err error) {
	ipmi, err := s.query("BIOS_LINCENSE_ACTIVATE.XML=(0,0)")
	if err != nil {
		return name, licType, err
	}

	if ipmi.BiosLicense != nil {
		switch ipmi.BiosLicense.Check {
		case "0":
			return "oob", "Activated", err
		case "1":
			return "oob", "Not Activated", err
		}
	}

	return name, licType, err
}

// Vendor returns bmc's vendor
func (s *SupermicroX) Vendor() (vendor string) {
	return supermicro.VendorID
}

// ServerSnapshot do best effort to populate the server data and returns a blade or discrete
// nolint: gocyclo
func (s *SupermicroX) ServerSnapshot() (server interface{}, err error) {
	if isBlade, _ := s.IsBlade(); isBlade {
		blade := &devices.Blade{}
		blade.Vendor = s.Vendor()
		blade.BmcAddress = s.ip
		blade.BmcType = s.HardwareType()

		blade.Serial, err = s.Serial()
		if err != nil {
			return nil, err
		}
		blade.BmcVersion, err = s.Version()
		if err != nil {
			return nil, err
		}
		blade.Model, err = s.Model()
		if err != nil {
			return nil, err
		}
		blade.Nics, err = s.Nics()
		if err != nil {
			return nil, err
		}
		blade.Disks, err = s.Disks()
		if err != nil {
			return nil, err
		}
		blade.BiosVersion, err = s.BiosVersion()
		if err != nil {
			return nil, err
		}
		blade.Processor, blade.ProcessorCount, blade.ProcessorCoreCount, blade.ProcessorThreadCount, err = s.CPU()
		if err != nil {
			return nil, err
		}
		blade.Memory, err = s.Memory()
		if err != nil {
			return nil, err
		}
		blade.Status, err = s.Status()
		if err != nil {
			return nil, err
		}
		blade.Name, err = s.Name()
		if err != nil {
			return nil, err
		}
		blade.TempC, err = s.TempC()
		if err != nil {
			return nil, err
		}
		blade.PowerKw, err = s.PowerKw()
		if err != nil {
			return nil, err
		}
		blade.PowerState, err = s.PowerState()
		if err != nil {
			return nil, err
		}
		blade.BmcLicenceType, blade.BmcLicenceStatus, err = s.License()
		if err != nil {
			return nil, err
		}
		blade.BladePosition, err = s.Slot()
		if err != nil {
			return nil, err
		}
		blade.ChassisSerial, err = s.ChassisSerial()
		if err != nil {
			return nil, err
		}
		server = blade
	} else {
		discrete := &devices.Discrete{}
		discrete.Vendor = s.Vendor()
		discrete.BmcAddress = s.ip
		discrete.BmcType = s.HardwareType()

		discrete.Serial, err = s.Serial()
		if err != nil {
			return nil, err
		}
		discrete.BmcVersion, err = s.Version()
		if err != nil {
			return nil, err
		}
		discrete.Model, err = s.Model()
		if err != nil {
			return nil, err
		}
		discrete.Nics, err = s.Nics()
		if err != nil {
			return nil, err
		}
		discrete.Disks, err = s.Disks()
		if err != nil {
			return nil, err
		}
		discrete.BiosVersion, err = s.BiosVersion()
		if err != nil {
			return nil, err
		}
		discrete.Processor, discrete.ProcessorCount, discrete.ProcessorCoreCount, discrete.ProcessorThreadCount, err = s.CPU()
		if err != nil {
			return nil, err
		}
		discrete.Memory, err = s.Memory()
		if err != nil {
			return nil, err
		}
		discrete.Status, err = s.Status()
		if err != nil {
			return nil, err
		}
		discrete.Name, err = s.Name()
		if err != nil {
			return nil, err
		}
		discrete.TempC, err = s.TempC()
		if err != nil {
			return nil, err
		}
		discrete.PowerKw, err = s.PowerKw()
		if err != nil {
			return nil, err
		}
		discrete.PowerState, err = s.PowerState()
		if err != nil {
			return nil, err
		}
		discrete.BmcLicenceType, discrete.BmcLicenceStatus, err = s.License()
		if err != nil {
			return nil, err
		}
		server = discrete
	}

	return server, err
}

// Disks returns a list of disks installed on the device
func (s *SupermicroX) Disks() (disks []*devices.Disk, err error) {
	return disks, err
}

// UpdateCredentials updates login credentials
func (s *SupermicroX) UpdateCredentials(username string, password string) {
	s.username = username
	s.password = password
}

// BiosVersion returns the BIOS version from the BMC, implements the Firmware interface
func (s *SupermicroX) GetBIOSVersion(ctx context.Context) (string, error) {
	return "", errors.ErrNotImplemented
}

// BMCVersion returns the BMC version, implements the Firmware interface
func (s *SupermicroX) GetBMCVersion(ctx context.Context) (string, error) {
	return "", errors.ErrNotImplemented
}

// Updates the BMC firmware, implements the Firmware interface
func (s *SupermicroX) FirmwareUpdateBMC(ctx context.Context, filePath string) error {
	return errors.ErrNotImplemented
}
