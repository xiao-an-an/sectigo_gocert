package sectigo_gocert

import (
		"github.com/hashicorp/terraform/helper/schema"
		"log"
		"crypto/x509"
		"crypto/x509/pkix"
		"fmt"
		"crypto/rand"
		"crypto/rsa"
		"encoding/asn1"
		"encoding/pem"
		"os"
		"io/ioutil"
		"strings"
		"net/http"
		"bytes"
		"encoding/json"
		"strconv"
		"time"

		"crypto/ecdsa"
		"crypto/elliptic"
		"flag"
)

// To get the SSLID from Enroll Cert Response Status
type EnrollResponseType struct {
	RenewId string `json:"renewId"`
	SslIdVal int   `json:"sslId"`
}

// To get the SSLID from Enroll Cert Response Status
type DownloadResponseType struct {
	DlCode int `json:"code"`
	Desc string   `json:"description"`
}

var oidemail_address = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 1}

var (
	host       = flag.String("host", "trianzcloud.com", "Comma-separated hostnames and IPs to generate a certificate for")
	validFrom  = flag.String("start-date", "", "Creation date formatted as Jan 1 15:04:05 2011")
	validFor   = flag.Duration("duration", 365*24*time.Hour, "Duration that certificate is valid for")
	isCA       = flag.Bool("ca", false, "whether this cert should be its own Certificate Authority")
	rsaBits    = flag.Int("rsa-bits", 2048, "Size of RSA key to generate. Ignored if --ecdsa-curve is set")
	ecdsaCurve = flag.String("ecdsa-curve", "P256", "ECDSA curve to use to generate a key. Valid values are P224, P256 (recommended), P384, P521")
)

// PEM Block for Key Generation
func pemBlockForKey(priv interface{}) *pem.Block {
	switch k := priv.(type) {
	case *rsa.PrivateKey:
		return &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}
	case *ecdsa.PrivateKey:
		b, err := x509.MarshalECPrivateKey(k)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Unable to marshal ECDSA private key: %v", err)
			os.Exit(2)
		}
		return &pem.Block{Type: "EC PRIVATE KEY", Bytes: b}
	default:
		return nil
	}
}

// Generate Key
func GenerateKey(d *schema.ResourceData, m interface{}) (interface{}, string) {

	domain := d.Get("domain").(string)
	cert_file_path := d.Get("cert_file_path").(string)

	log.Println("Generating KEY for "+domain)
	WriteLogs(d,"Generating KEY for "+domain)

	// keyBytes, err := rsa.GenerateKey(rand.Reader, 2048)

	// // Write KEY to a file 
	// keyOut, err := os.Create(cert_file_path+domain+".key")
	// if err != nil {
	// 	log.Println("Failed to open ca.key for writing:", err)
	// 	WriteLogs(d,"Failed to open ca.key for writing:"+err.Error())
	// 	CleanUp(d)
	// 	os.Exit(1)
	// }
	// pem.Encode(keyOut, &pem.Block{
	// 		Type:  "RSA PRIVATE KEY",
	// 		Bytes: x509.MarshalPKCS1PrivateKey(keyBytes),
	// })
	// keyOut.Close()

	var priv interface{}
	var err error
	switch *ecdsaCurve {
	case "":
		fmt.Println("log1")
		priv, err = rsa.GenerateKey(rand.Reader, *rsaBits)
	case "P224":
		fmt.Println("log2")
		priv, err = ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
	case "P256":
		fmt.Println("log3")
		priv, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "P384":
		fmt.Println("log4")
		priv, err = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case "P521":
		fmt.Println("log5")
		priv, err = ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	default:
		fmt.Println("log6")
		fmt.Fprintf(os.Stderr, "Unrecognized elliptic curve: %q", *ecdsaCurve)
		os.Exit(1)
	}
	if err != nil {
		log.Fatalf("failed to generate private key: %s", err)
	}

	// Write key to file
	keyOut, err := os.OpenFile(cert_file_path+domain+".key", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		log.Print("Failed to open key.pem for writing:", err)
		WriteLogs(d,"Failed to open key.pem for writing: "+err.Error())
		CleanUp(d)
		os.Exit(1)
	}
	if err := pem.Encode(keyOut, pemBlockForKey(priv)); err != nil {
		log.Fatalf("Failed to write data to key.pem: %s", err)
		WriteLogs(d,"Failed to write data to key.pem: "+err.Error())
		CleanUp(d)
		os.Exit(1)
	}
	if err := keyOut.Close(); err != nil {
		log.Fatalf("error closing key.pem: %s", err)
		WriteLogs(d,"Error closing key.pem: "+err.Error())
		CleanUp(d)
		os.Exit(1)
	}

	//Read Key from file and put it in the tfstate
	keyVal, err := ioutil.ReadFile(cert_file_path+domain+".key")
	if err != nil {
		log.Println("Failed to read the ca.key from file:", err)
		WriteLogs(d,"Failed to read the ca.key from file:"+ err.Error())
		CleanUp(d)
		os.Exit(1)
	}
	//d.Set("sectigo_key",string(keyVal))
	return priv, string(keyVal)
}

// Generate CSR
func GenerateCSR(d *schema.ResourceData, m interface{}, keyBytes *rsa.PrivateKey) ([]byte, string) {

	domain := d.Get("domain").(string)
	cert_file_path := d.Get("cert_file_path").(string)

	log.Println("Generating CSR for "+domain)
	WriteLogs(d,"Generating CSR for "+domain)

	subj := pkix.Name{
        CommonName:         domain,
        Country:            []string{d.Get("country").(string)},
        Province:           []string{d.Get("province").(string)},
        Locality:           []string{d.Get("locality").(string)},
        Organization:       []string{d.Get("organization").(string)},
        OrganizationalUnit:	[]string{d.Get("org_unit").(string)},
        ExtraNames: []pkix.AttributeTypeAndValue{
            {
                Type:  oidemail_address, 
                Value: asn1.RawValue{
                    Tag:   asn1.TagIA5String, 
                    Bytes: []byte(d.Get("email_address").(string)),
                },
            },
        },
    }

    template := x509.CertificateRequest{
        Subject:            subj,
		SignatureAlgorithm: x509.SHA256WithRSA,
		DNSNames:			[]string{d.Get("subject_alt_names").(string)} ,
    }

    csrBytes, _ := x509.CreateCertificateRequest(rand.Reader, &template, keyBytes)

	// Put CSR in a file 
    csrOut, err := os.Create(cert_file_path+domain+".csr")
    if err != nil {
		log.Println("Failed to open CSR for writing:", err)
		WriteLogs(d,"Failed to open CSR for writing:"+err.Error())
		CleanUp(d)
        os.Exit(1)
    }
    pem.Encode(csrOut, &pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrBytes})
	csrOut.Close()

	//Read CSR from file and put it in the tfstate
	csrVal, err := ioutil.ReadFile(cert_file_path+domain+".csr")
	if err != nil {
		log.Println("Failed to write CSR to a file:", err)
		WriteLogs(d,"Failed to write CSR to a file:"+err.Error())
        os.Exit(1)
    }
	var csrString = strings.Replace(string(csrVal),"\n","",-1)
	// d.Set("sectigo_csr",string(csrVal))

	return csrVal, csrString
}

// Enroll Cert
func EnrollCert(d *schema.ResourceData,csrVal string, customerArr map[string]string) (int,string) {
	domain := d.Get("domain").(string)
	var sslId = 0
	var renewId = ""
	url := d.Get("sectigo_ca_base_url").(string)+"enroll"
	var jsonStr = []byte("{\"orgId\":"+strconv.Itoa(d.Get("sectigo_cm_orgid").(int))+",\"csr\":\""+csrVal+"\",\"certType\":"+strconv.Itoa(d.Get("cert_type").(int))+",\"numberServers\":"+strconv.Itoa(d.Get("cert_num_servers").(int))+",\"serverType\":"+strconv.Itoa(d.Get("server_type").(int))+",\"term\":"+strconv.Itoa(d.Get("cert_validity").(int))+",\"comments\":\""+d.Get("cert_comments").(string)+"\",\"externalRequester\":\""+d.Get("cert_ext_requester").(string)+"\",\"subjAltNames\":\""+d.Get("subject_alt_names").(string)+"\"}")	

	log.Println("Enrolling CERT for "+domain)
	WriteLogs(d,"Enrolling CERT for "+domain)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Login", customerArr["username"])
	req.Header.Set("Password", customerArr["password"])
	req.Header.Set("Customeruri", customerArr["customer_uri"])
    if err != nil {
		log.Println(err)
		WriteLogs(d,err.Error())
		CleanUp(d)
		os.Exit(1)
    }

    client := &http.Client{}
    resp, err := client.Do(req)
    if err != nil {
		//panic(err)
		log.Println(err)
		WriteLogs(d,err.Error())
		CleanUp(d)
		os.Exit(1)
    }
    defer resp.Body.Close()

    enrollResponse, err := ioutil.ReadAll(resp.Body)
    if err != nil {
		//panic(err)
		log.Println(err)
		WriteLogs(d,err.Error())
		CleanUp(d)
		os.Exit(1)
    }
	log.Println("Response Status:", resp.Status)
    log.Println("Enroll Response:", string(enrollResponse))
	WriteLogs(d,"Response Status:"+ resp.Status)
	WriteLogs(d,"Enroll Response:"+ string(enrollResponse))

	var enrollStatus = strings.Contains(resp.Status, "200")
	var sslIdStatus = strings.Contains(string(enrollResponse), "sslId")
	if enrollStatus && sslIdStatus {
		log.Println("Certificate succesfully Enrolled...")
		WriteLogs(d,"Certificate succesfully Enrolled...")
		
		// Fetch ssl id from response json
		var enrollResponseJson = []byte(string(enrollResponse))
		var enr EnrollResponseType
		json.Unmarshal(enrollResponseJson, &enr)
		sslId = enr.SslIdVal
		renewId = enr.RenewId
		if(string(sslId) != "" && sslId > 0) {
			log.Println(sslId)
			WriteLogs(d,strconv.Itoa(sslId))
		} else {
			log.Println("SSLID Generation Failed... Exiting..."+string(enrollResponse))
			WriteLogs(d,"SSLID Generation Failed... Exiting..."+string(enrollResponse))
			CleanUp(d)
			os.Exit(1)
		}
	} else {
		log.Println("Certificate Enrollment Failed... Exiting..."+string(enrollResponse))
		WriteLogs(d,"Certificate Enrollment Failed... Exiting..."+string(enrollResponse))
		CleanUp(d)
		os.Exit(1)
	}
	return sslId,renewId
}

// DOWNLOAD CERT 
func DownloadCert(sslId int, d *schema.ResourceData, customerArr map[string]string, timer int) string {
	max_timeout := d.Get("max_timeout").(int)
	//max_timeout, err := strconv.Atoi(max_timeout1)
	domain := d.Get("domain").(string)
	cert_file_path := d.Get("cert_file_path").(string)
	url := d.Get("sectigo_ca_base_url").(string)+"collect/"+strconv.Itoa(sslId)+"/x509CO"

	log.Println("---------DOWNLOAD CERT for "+domain+"---------")
	log.Println(url)
	WriteLogs(d,"---------DOWNLOAD CERT for "+domain+"---------")
	WriteLogs(d,url)

	req, err := http.NewRequest("GET", url, nil)	
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Login", customerArr["username"])
	req.Header.Set("Password", customerArr["password"])
	req.Header.Set("Customeruri", customerArr["customer_uri"])

	client := &http.Client{}
    resp, err := client.Do(req)
    if err != nil {
		//panic(err)
		log.Println(err)
		WriteLogs(d,err.Error())
		CleanUp(d)
		os.Exit(1)
    }
    defer resp.Body.Close()

    downloadResponse, _ := ioutil.ReadAll(resp.Body)
	log.Println("Response Status:", resp.Status)
    log.Println("Download Response:", string(downloadResponse))
	WriteLogs(d,"Response Status:"+ resp.Status)
	WriteLogs(d,"Download Response:"+ string(downloadResponse))
	
	// Fetch code and reason from downloadresponse json
	var downloadResponseJson = []byte(string(downloadResponse))
	var dl DownloadResponseType
	json.Unmarshal(downloadResponseJson, &dl)
	var dlCode = dl.DlCode
	if(dlCode != 0) && (dlCode != -1400) {
		log.Println("Cert code <"+strconv.Itoa(dlCode)+": "+dl.Desc+"> not valid. Process not complete. Exiting.")
		WriteLogs(d,"Cert code <"+strconv.Itoa(dlCode)+": "+dl.Desc+"> not valid. Process not complete. Exiting.")
		CleanUp(d)
		os.Exit(1)	
	}
	
	var downloadStatus = strings.Contains(resp.Status, "200")
	if downloadStatus {
		// Write crt to file		
		f, err := os.Create(cert_file_path+domain+".crt")
		if err != nil {
			log.Println(err)
			WriteLogs(d,err.Error())
			CleanUp(d)
			os.Exit(1)
		}
		l, err := f.WriteString(string(downloadResponse))
		if err != nil {
			log.Println(err)
			WriteLogs(d,err.Error())
			f.Close()
			CleanUp(d)
			os.Exit(1)
		}
		log.Println(l, "bytes written successfully")
		err = f.Close()
		if err != nil {
			log.Println(err)
			WriteLogs(d,err.Error())
			CleanUp(d)
			os.Exit(1)
		}

		//Write CERT and SSLID to statefile
		//d.Set("sectigo_crt",string(downloadResponse))
		//d.Set("sectigo_ssl_id",strconv.Itoa(sslId))

		return string(downloadResponse)
	} else {
		timer = timer + d.Get("loop_period").(int)
		log.Println("Waiting for "+strconv.Itoa(timer)+" / "+strconv.Itoa(max_timeout)+" seconds before the next download attempt...")
		WriteLogs(d,"Waiting for "+strconv.Itoa(timer)+" / "+strconv.Itoa(max_timeout)+" seconds before the next download attempt...")
		time.Sleep(time.Duration(d.Get("loop_period").(int)) * time.Second)
		if(timer >= max_timeout){
			log.Println("Timed out!! Waited for "+strconv.Itoa(timer)+"/"+strconv.Itoa(max_timeout)+" seconds. You can increase/decrease the timeout period (in seconds) in the tfvars file")
			log.Println("Download Response:", string(downloadResponse))
			WriteLogs(d,"Timed out!! Waited for "+strconv.Itoa(timer)+"/"+strconv.Itoa(max_timeout)+" seconds. You can increase/decrease the timeout period (in seconds) in the tfvars file")
			WriteLogs(d,"Download Response:"+string(downloadResponse))

			if(dlCode == 0) || (dlCode == -1400) {
				return "TimedOutStateSaved"				
			} else {
				CleanUp(d)
				os.Exit(1)	
			}
		} else {
			return DownloadCert(sslId,d,customerArr,timer)
		}
	}
	return string(downloadResponse)
}

// Revoke Certificate 
func RevokeCertificate(sslId int, d *schema.ResourceData, customerArr map[string]string) (bool, error) {
	var flg = false 
	url := d.Get("sectigo_ca_base_url").(string)+"revoke/"+strconv.Itoa(sslId)
	log.Println(url)
	WriteLogs(d,url)
	var jsonStr = []byte("{\"reason\":\"Terraform destroy\"}")

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
    if err != nil {
		log.Println(err)
		WriteLogs(d,err.Error())
		os.Exit(1)
    }
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Login", customerArr["username"])
	req.Header.Set("Password", customerArr["password"])
	req.Header.Set("Customeruri", customerArr["customer_uri"])

    client := &http.Client{}
    resp, err := client.Do(req)
    if err != nil {
		//panic(err)
		log.Println(err)
		WriteLogs(d,err.Error())
		CleanUp(d)
		os.Exit(1)
    }
    defer resp.Body.Close()

	revokeResponse, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		//panic(err)
		log.Println(err)
		WriteLogs(d,err.Error())
		CleanUp(d)
		os.Exit(1)
    }
	log.Println("Revoke Response Status:", resp.Status)
    log.Println("Revoke Response:", string(revokeResponse))
	WriteLogs(d,"Revoke Response Status:"+ resp.Status)
    WriteLogs(d,"Revoke Response:"+string(revokeResponse))

	var revokeStatus = strings.Contains(resp.Status, "204")
	if revokeStatus  {
		log.Println("Certificate successfully Revoked...")
		WriteLogs(d,"Certificate successfully Revoked...")
		CleanUp(d)
		flg = true
	} else {
		os.Exit(1)
	}
	return flg, err
}

// Get Env value
func GetProviderEnvValue(d *schema.ResourceData,param string, envParam string) string {

	val := os.Getenv(envParam)
	if val == ""  {
		log.Println(param+" Variable \""+envParam+"\" not set or empty. Please set the password in TFVARS file or as Environment Variable and try again.")
		WriteLogs(d,param+" Variable \""+envParam+"\" not set or empty. Please set the password in TFVARS file or as Environment Variable and try again.")
		CleanUp(d)
		os.Exit(1)
	} 
	// log.Println(val)
	// WriteLogs(d,val)
	val = strings.Replace(string(val),"\r","",-1)
	return string(val)
}

// Get Param value
func GetParamValue(d *schema.ResourceData,param string, envParam string) string {

	val1 := d.Get(param).(string)
	if (val1 == "") {
		val2, exists := os.LookupEnv(envParam)
		if val2 == "" || !exists {
			log.Println(param+" Variable \""+envParam+"\" not set or empty. Please set the password in TFVARS file or as Environment Variable and try again.")
			WriteLogs(d,param+" Variable \""+envParam+"\" not set or empty. Please set the password in TFVARS file or as Environment Variable and try again.")
			CleanUp(d)
			os.Exit(1)
		} else{
			val1 = val2
		}		
	} 
	//log.Println(val1)
	//WriteLogs(d,val1)
	val := strings.Replace(string(val1),"\r","",-1)
	return val
}

// Write Logs to a file
func WriteLogs(d *schema.ResourceData,log string){
	current := time.Now()
	domain := d.Get("domain").(string)
	cert_file_path := d.Get("cert_file_path").(string)
		
	// If the file doesn't exist, create it, or append to the file
	file, err := os.OpenFile(cert_file_path+domain+".log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println(err)
	}
	if _, err := file.Write([]byte(current.Format("2006-01-02 15:04:05")+" "+log+"\n")); err != nil {
		fmt.Println(err)
	}
	if err := file.Close(); err != nil {
		fmt.Println(err)
	}	
}

// Clean up the previous files
func CleanUp(d *schema.ResourceData,params ...string){
	domain := d.Get("domain").(string)
	cert_file_path := d.Get("cert_file_path").(string)

	if len(params) > 0 {
		var oldDomain = params[0]
		os.Remove(cert_file_path+oldDomain+".csr")
		os.Remove(cert_file_path+oldDomain+".crt")
		os.Remove(cert_file_path+oldDomain+".key")
		log.Println("Deleting any previous CSR/KEY/CERT that was generated")
		WriteLogs(d,"Deleting any previous CSR/KEY/CERT that was generated")
	} else{
		os.Remove(cert_file_path+domain+".csr")
		os.Remove(cert_file_path+domain+".crt")
		os.Remove(cert_file_path+domain+".key")	
		log.Println("Could not complete the process. Deleting any CSR/KEY/CERT that was generated")
		WriteLogs(d,"Could not complete the process. Deleting any CSR/KEY/CERT that was generated")
	}

}

// Returns whether the given file or directory exists
func PathExists(path string) (bool, error) {
    _, err := os.Stat(path)
    if err == nil { return true, nil }
    if os.IsNotExist(err) { return false, nil }
    return true, err
}