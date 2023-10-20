package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

type etherscanContract struct {
	Name             string
	DeployedAddress  string
	Abi              string
	DeployedBytecode string
	InitBytecode     string
}

type unverifiedContract struct {
	Name             string
	DeploymentTxHash string
	DeployedAddress  string
	Abi              string
	DeployedBytecode string
	InitBytecode     string
}

type immutableContract struct {
	Name             string
	DeploymentTxHash string
	DeployedAddress  string
	Abi              string
	InitBytecode     string
}

type etherscanContracts struct {
	Verified struct {
		StandardDeployment []etherscanContract `json:"standardDeployment"`
		ProxyDeployment    []etherscanContract `json:"proxyDeployment"`
	}
	Unverified []unverifiedContract `json:"unverified"`
	Immutables struct {
		ProxyDeployment []immutableContract `json:"proxyDeployment"`
	}
}

type etherscanApiResponse struct {
	Status  string      `json:"status"`
	Message string      `json:"message"`
	Result  interface{} `json:"result"`
}

type txInfo struct {
	Input string `json:"input"`
}

type etherscanRpcResponse struct {
	JsonRpc string      `json:"jsonrpc"`
	Id      int         `json:"id"`
	Result  interface{} `json:"result"`
}

type contractMetadata struct {
	Name        string
	DeployedBin string
	Package     string
}

type contractWithImmutablesMetadata struct {
	Name           string
	InitBin        string
	Package        string
	DeploymentSalt string
}

const (
	etherscanGetAbiUrl              = "https://api.etherscan.io/api?module=contract&action=getabi&address=%s&apikey=%s"
	etherscanGetDeploymentTxHashUrl = "https://api.etherscan.io/api?module=contract&action=getcontractcreation&contractaddresses=%s&apikey=%s "
	etherscanGetDeployedBytecodeUrl = "https://api.etherscan.io/api?module=proxy&action=eth_getCode&address=%s&tag=latest&apikey=%s"
	etherscanGetTxByHashUrl         = "https://api.etherscan.io/api?module=proxy&action=eth_getTransactionByHash&txHash=%s&tag=latest&apikey=%s"
)

// readEtherscanContractsList reads a JSON file specified by the given file path and
// parses it into a slice of `etherscanContract`.
//
// Parameters:
// - filePath: The path to the JSON file containing the list of etherscan contracts.
//
// Returns:
// - A slice of etherscanContract parsed from the JSON file.
// - An error if reading the file or parsing the JSON failed.
func readEtherscanContractsList(filePath string) (etherscanContracts, error) {
	var data contractsData
	err := readJSONFile(filePath, &data)
	return data.Etherscan, err
}

// fetchHttp sends an HTTP GET request to the provided URL and
// retrieves the response body. The function returns the body as a byte slice.
//
// Parameters:
//   - url: The target URL for the HTTP GET request.
//
// Returns:
//   - A byte slice containing the response body.
//   - An error if there was an issue with the HTTP request or reading the response.
//
// Note:
//
//	The caller is responsible for interpreting the returned data, including
//	unmarshaling it if it represents structured data (e.g., JSON).
func fetchHttp(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// fetchEtherscanApi makes a request to the given Etherscan API URL.
// It performs retries up to `apiMaxRetries` if the API responds with a rate limit error.
// Between retries, it waits for `apiRetryDelay` seconds.
//
// Parameters:
//   - url (string): The Etherscan API endpoint to fetch data from.
//   - apiMaxRetries (int): The maximum number of retries in case of a rate limit error.
//   - apiRetryDelay (int): The delay in seconds between retries.
//
// Returns:
// - etherscanApiResponse: A struct representing the response from the Etherscan API.
// It expects the API to return a JSON structure with at least a
// `Message` field indicating the status of the request (either `OK`
// or `NOTOK`) and a `Result` field with potential error details.
// - error: An error indicating any issues that occurred during the request or the
// unmarshalling of the response. If the maximum number of retries is exceeded
// without a successful request, an error is returned indicating the number of
// failed attempts.
//
// Notes:
//   - If the API responds with any message other than `OK`, the function returns an error.
//   - If the API responds with a `Max rate limit reached` message, the function waits and retries
//     the request.
//   - All other errors or unexpected responses will cause the function to return immediately with
//     an error.
func fetchEtherscanApi(url string, apiMaxRetries, apiRetryDelay int) (etherscanApiResponse, error) {
	var maxRetries = apiMaxRetries
	var retryDelay = time.Duration(apiRetryDelay) * time.Second

	for retries := 0; retries < maxRetries; retries++ {
		body, err := fetchHttp(url)
		if err != nil {
			return etherscanApiResponse{}, err
		}

		var apiResponse etherscanApiResponse
		err = json.Unmarshal(body, &apiResponse)
		if err != nil {
			log.Printf("Failed to unmarshal as etherscanApiResponse: %v", err)
			return etherscanApiResponse{}, err
		}

		if apiResponse.Message == "NOTOK" && apiResponse.Result == "Max rate limit reached" {
			log.Printf("Reached API rate limit, waiting %v and trying again", retryDelay)
			time.Sleep(retryDelay)
			continue
		}

		if apiResponse.Message != "OK" {
			return etherscanApiResponse{}, fmt.Errorf("There was an issue with the Etherscan request to %s, received response: %v", url, apiResponse)
		}

		return apiResponse, nil
	}

	return etherscanApiResponse{}, fmt.Errorf("Failed to fetch from Etherscan API after %d retries", maxRetries)
}

func fetchEtherscanRpc(url string, apiMaxRetries, apiRetryDelay int) (etherscanRpcResponse, error) {
	var maxRetries = apiMaxRetries
	var retryDelay = time.Duration(apiRetryDelay) * time.Second

	for retries := 0; retries < maxRetries; retries++ {
		body, err := fetchHttp(url)
		if err != nil {
			return etherscanRpcResponse{}, err
		}

		var rpcResponse etherscanRpcResponse
		err = json.Unmarshal(body, &rpcResponse)
		if err != nil {
			log.Printf("Failed to unmarshal response as etherscanRpcResponse, waiting %v and trying again", retryDelay)
			time.Sleep(retryDelay)
			continue
		}

		if rpcResponse.Result == "Max rate limit reached" {
			log.Printf("Reached API rate limit, waiting %v and trying again", retryDelay)
			time.Sleep(retryDelay)
			continue
		}

		return rpcResponse, nil
	}

	return etherscanRpcResponse{}, fmt.Errorf("Failed to fetch from Etherscan RPC after %d retries", maxRetries)
}

// fetchAbi retrieves a contract's ABI using the given Etherscan API URL.
// The function utilizes `fetchEtherscanApi` to communicate with the API and handle any
// rate limit errors with retries.
//
// Parameters:
// - url (string): The Etherscan API endpoint to fetch the ABI from.
// - apiMaxRetries (int): The maximum number of retries in case of a rate limit error or
// other recoverable issues.
// - apiRetryDelay (int): The delay in seconds between retries.
//
// Returns:
// - string: The ABI string as provided by the Etherscan API. If the API response does not
// contain the ABI as a string, or if there are any other issues during the request,
// an empty string is returned.
//
// - error: An error indicating any problems that occurred during the request, the unmarshalling
// of the response, or if the result is not a string as expected. Successful requests
// will return a nil error.
//
// Notes:
//   - The function relies on the `fetchEtherscanApi` to handle retries and rate limits.
//   - The ABI is expected to be returned as a string in the `Result` field of the API response.
//     If the `Result` field contains data other than a string, an error is returned.
func fetchAbi(url string, apiMaxRetries, apiRetryDelay int) (string, error) {
	response, err := fetchEtherscanApi(url, apiMaxRetries, apiRetryDelay)
	if err != nil {
		return "", err
	}

	abi, ok := response.Result.(string)
	if !ok {
		return "", fmt.Errorf("API response result is not expected ABI string")
	}

	return abi, nil
}

func fetchDeployedBytecode(url string, apiMaxRetries, apiRetryDelay int) (string, error) {
	response, err := fetchEtherscanRpc(url, apiMaxRetries, apiRetryDelay)
	if err != nil {
		return "", err
	}

	bytecode, ok := response.Result.(string)
	if !ok {
		return "", fmt.Errorf("API response result is not expected bytecode string")
	}

	return bytecode, nil
}

// fetchDeploymentTxHash retrieves the deployment transaction hash for a given contract using the given Etherscan API URL.
// The function utilizes `fetchEtherscanApi` to communicate with the API and handle any
// rate limit errors with retries.
//
// Parameters:
// - url (string): The Etherscan API endpoint to fetch the ABI from.
// - apiMaxRetries (int): The maximum number of retries in case of a rate limit error or
// other recoverable issues.
// - apiRetryDelay (int): The delay in seconds between retries.
//
// Returns:
// - string: The deployment transaction hash for the given contract. If there are any issues during the request,
// or if the expected data isn't found in the response, an empty string is returned.
//
// - error: An error indicating any problems that occurred during the request, the unmarshalling of the response,
// or if the expected data structures are not present in the response.
//
// Notes:
//   - The function assumes that the `Result` field of the Etherscan API response will contain a slice of transaction
//     info objects and that the deployment transaction hash will be present in the `txHash` field of the first transaction
//     info object.
//   - If the `Result` field doesn't contain a slice of expected objects or if the `txHash` field isn't found or isn't a
//     string, an error is returned.
func fetchDeploymentTxHash(url string, apiMaxRetries, apiRetryDelay int) (string, error) {
	response, err := fetchEtherscanApi(url, apiMaxRetries, apiRetryDelay)
	if err != nil {
		return "", err
	}

	results, ok := response.Result.([]interface{})
	if !ok {
		return "", fmt.Errorf("Failed to assert API response result is type of []etherscanDeploymentTxInfo")
	}
	if len(results) == 0 {
		return "", fmt.Errorf("API response result is an empty array")
	}

	deploymentTxInfo, ok := results[0].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("Failed to assert API response result[0] is type of etherscanDeploymentTxInfo")
	}

	txHash, ok := deploymentTxInfo["txHash"].(string)
	if !ok {
		return "", fmt.Errorf("Failed to assert API response result[0][\"txHash\"] is type of string")
	}

	return txHash, nil
}

// fetchDeploymentData retrieves the deployment data for a given contract using the given Etherscan RPC URL.
//
// Parameters:
// - url (string): The Etherscan RPC endpoint from which the deployment data should be fetched.
// - apiMaxRetries (int): The maximum number of retries if there's a rate limit error or other recoverable issues.
// - apiRetryDelay (int): The delay in seconds between retries.
//
// Returns:
// - string: The deployment data for the given contract. If there are any issues during the request or if the expected
// data isn't found in the response, an empty string is returned.
// - error: An error indicating any problems that occurred during the request, the unmarshalling of the response, or
// if the expected `Input` field is not present in the RPC response.
//
// Notes:
//   - The function assumes that the response from the Etherscan RPC endpoint will contain a field named `Input`
//     that has the deployment data of.
//   - The function will retry the request up to `apiMaxRetries` times if there is an error unmarshalling the response.
//     Between retries, the function will wait for the specified `apiRetryDelay` duration.
func fetchDeploymentData(url string, apiMaxRetries, apiRetryDelay int) (string, error) {
	response, err := fetchEtherscanRpc(url, apiMaxRetries, apiRetryDelay)
	if err != nil {
		return "", err
	}

	resultBytes, err := json.Marshal(response.Result)
	if err != nil {
		return "", fmt.Errorf("Failed to marshal Result into JSON: %v", err)
	}

	var tx txInfo
	err = json.Unmarshal(resultBytes, &tx)
	if err != nil {
		return "", fmt.Errorf("API response result is not expected txInfo struct: %v", err)
	}

	return tx.Input, nil
}

// writeContractMetadata writes the provided `etherscanContractMetadata`
// to a file using the provided `fileTemplate`.
// The file is named after the contract (with the contract name transformed to lowercase),
// and has the "_more.go" suffix.
//
// Parameters:
// - contractMetaData: An instance of `etherscanContractMetadata` containing metadata details of the contract.
// - metadataOutputDir: The directory where the metadata file should be saved.
// - contractName: The name of the contract for which the metadata is being written.
// - fileTemplate: A pointer to a `template.Template` used to format and write the metadata to the file.
//
// Returns:
// - An error if there's an issue opening the metadata file, executing the template, or writing to the file.
func writeContractMetadata(contractMetaData contractMetadata, metadataOutputDir, contractName string, fileTemplate *template.Template) error {
	metaDataFilePath := filepath.Join(metadataOutputDir, strings.ToLower(contractName)+"_more.go")
	metadataFile, err := os.OpenFile(
		metaDataFilePath,
		os.O_RDWR|os.O_CREATE|os.O_TRUNC,
		0o600,
	)
	if err != nil {
		return fmt.Errorf("Error opening %s's metadata file at %s: %w", contractName, metaDataFilePath, err)
	}
	defer metadataFile.Close()

	if err := fileTemplate.Execute(metadataFile, contractMetaData); err != nil {
		return fmt.Errorf("Error writing %s's contract metadata at %s: %w", contractName, metaDataFilePath, err)
	}

	log.Printf("Wrote %s's contract metadata to: %s", contractName, metaDataFilePath)
	return nil
}

func writeContractWithImmutablesMetadata(contractMetaData contractWithImmutablesMetadata, metadataOutputDir, contractName string, fileTemplate *template.Template) error {
	metaDataFilePath := filepath.Join(metadataOutputDir, strings.ToLower(contractName)+"_more.go")
	metadataFile, err := os.OpenFile(
		metaDataFilePath,
		os.O_RDWR|os.O_CREATE|os.O_TRUNC,
		0o600,
	)
	if err != nil {
		return fmt.Errorf("Error opening %s's metadata file at %s: %w", contractName, metaDataFilePath, err)
	}
	defer metadataFile.Close()

	if err := fileTemplate.Execute(metadataFile, contractMetaData); err != nil {
		return fmt.Errorf("Error writing %s's contract metadata at %s: %w", contractName, metaDataFilePath, err)
	}

	log.Printf("Wrote %s's contract metadata to: %s", contractName, metaDataFilePath)
	return nil
}

// genEtherscanBindings generates Go bindings for Ethereum smart contracts based on the ABI and bytecode
// fetched from Etherscan.
// The function reads the list of contracts from the provided file path and fetches the ABI and
// bytecode for each contract from Etherscan using the provided API key. It then generates Go bindings
// for each contract and writes metadata for each contract to the specified output directory.
//
// Parameters:
// - contractListFilePath: Path to the file containing the list of contracts.
// - sourceMapsListStr: Comma-separated list of source maps.
// - etherscanApiKey: API key to fetch data from Etherscan.
// - goPackageName: Name of the Go package for the generated bindings.
// - metadataOutputDir: Directory to output the generated contract metadata.
//
// Returns:
//   - An error if there are issues reading the contract list, fetching data from Etherscan, generating
//     contract bindings, or writing contract metadata.
func genEtherscanBindings(contractListFilePath, sourceMapsListStr, etherscanApiKey, goPackageName, metadataOutputDir string, apiMaxRetries, apiRetryDelay int) error {
	contracts, err := readEtherscanContractsList(contractListFilePath)
	if err != nil {
		return fmt.Errorf("Error reading contract list %s: %w", contractListFilePath, err)
	}

	// TODO - Reimplement
	// if len(contracts.Verified) == 0 && len(contracts.Unverified) == 0 {
	// 	return fmt.Errorf("No contracts parsable from given contract list: %s", contractListFilePath)
	// }

	tempArtifactsDir, err := mkTempArtifactsDir()
	if err != nil {
		return err
	}
	defer func() {
		err := os.RemoveAll(tempArtifactsDir)
		if err != nil {
			log.Printf("Error removing temporary directory %s: %v", tempArtifactsDir, err)
		} else {
			log.Printf("Successfully removed temporary directory")
		}
	}()

	contractMetadataFileTemplate := template.Must(template.New("contractMetadata").Parse(contractMetadataTemplate))

	sourceMapsList := strings.Split(sourceMapsListStr, ",")
	sourceMapsSet := make(map[string]struct{})
	for _, k := range sourceMapsList {
		sourceMapsSet[k] = struct{}{}
	}

	for _, contract := range contracts.Verified.StandardDeployment {
		log.Printf("Generating bindings and metadata for Etherscan contract with standard deployment: %s", contract.Name)

		contract.Abi, err = fetchAbi(fmt.Sprintf(etherscanGetAbiUrl, contract.DeployedAddress, etherscanApiKey), apiMaxRetries, apiRetryDelay)
		if err != nil {
			return err
		}

		contract.DeployedBytecode, err = fetchDeployedBytecode(fmt.Sprintf(etherscanGetDeployedBytecodeUrl, contract.DeployedAddress, etherscanApiKey), apiMaxRetries, apiRetryDelay)
		if err != nil {
			return err
		}

		deploymentTxHash, err := fetchDeploymentTxHash(fmt.Sprintf(etherscanGetDeploymentTxHashUrl, contract.DeployedAddress, etherscanApiKey), apiMaxRetries, apiRetryDelay)
		if err != nil {
			return err
		}
		contract.InitBytecode, err = fetchDeploymentData(fmt.Sprintf(etherscanGetTxByHashUrl, deploymentTxHash, etherscanApiKey), apiMaxRetries, apiRetryDelay)
		if err != nil {
			return err
		}

		abiFilePath, bytecodeFilePath, err := writeContractArtifacts(tempArtifactsDir, contract.Name, []byte(contract.Abi), []byte(contract.InitBytecode))
		if err != nil {
			return err
		}

		err = genContractBindings(abiFilePath, bytecodeFilePath, goPackageName, contract.Name)
		if err != nil {
			return err
		}

		contractMetaData := contractMetadata{
			Name:        contract.Name,
			DeployedBin: contract.DeployedBytecode,
			Package:     goPackageName,
		}

		if err := writeContractMetadata(contractMetaData, metadataOutputDir, contract.Name, contractMetadataFileTemplate); err != nil {
			return err
		}
	}

	for _, contract := range contracts.Verified.ProxyDeployment {
		log.Printf("Generating bindings and metadata for Etherscan contract with proxy deployment: %s", contract.Name)

		contract.Abi, err = fetchAbi(fmt.Sprintf(etherscanGetAbiUrl, contract.DeployedAddress, etherscanApiKey), apiMaxRetries, apiRetryDelay)
		if err != nil {
			return err
		}

		contract.DeployedBytecode, err = fetchDeployedBytecode(fmt.Sprintf(etherscanGetDeployedBytecodeUrl, contract.DeployedAddress, etherscanApiKey), apiMaxRetries, apiRetryDelay)
		if err != nil {
			return err
		}

		deploymentTxHash, err := fetchDeploymentTxHash(fmt.Sprintf(etherscanGetDeploymentTxHashUrl, contract.DeployedAddress, etherscanApiKey), apiMaxRetries, apiRetryDelay)
		if err != nil {
			return err
		}
		proxyDeploymentData, err := fetchDeploymentData(fmt.Sprintf(etherscanGetTxByHashUrl, deploymentTxHash, etherscanApiKey), apiMaxRetries, apiRetryDelay)
		if err != nil {
			return err
		}
		// Assuming proxy deployment was using Arachnid's (or Safe's) deployer,
		// so first 32 bytes of tx input data is used at salt, and remaining data is the contract init bytecode
		contract.InitBytecode = string([]rune(proxyDeploymentData)[66:])

		abiFilePath, bytecodeFilePath, err := writeContractArtifacts(tempArtifactsDir, contract.Name, []byte(contract.Abi), []byte(contract.InitBytecode))
		if err != nil {
			return err
		}

		err = genContractBindings(abiFilePath, bytecodeFilePath, goPackageName, contract.Name)
		if err != nil {
			return err
		}

		contractMetaData := contractMetadata{
			Name:        contract.Name,
			DeployedBin: contract.DeployedBytecode,
			Package:     goPackageName,
		}

		if err := writeContractMetadata(contractMetaData, metadataOutputDir, contract.Name, contractMetadataFileTemplate); err != nil {
			return err
		}
	}

	for _, contract := range contracts.Unverified {
		log.Printf("Generating bindings and metadata for unverified Etherscan contract: %s", contract.Name)

		contract.DeployedBytecode, err = fetchDeployedBytecode(fmt.Sprintf(etherscanGetDeployedBytecodeUrl, contract.DeployedAddress, etherscanApiKey), apiMaxRetries, apiRetryDelay)
		if err != nil {
			return err
		}

		contract.InitBytecode, err = fetchDeploymentData(fmt.Sprintf(etherscanGetTxByHashUrl, contract.DeploymentTxHash, etherscanApiKey), apiMaxRetries, apiRetryDelay)
		if err != nil {
			return err
		}

		abiFilePath, bytecodeFilePath, err := writeContractArtifacts(tempArtifactsDir, contract.Name, []byte(contract.Abi), []byte(contract.InitBytecode))
		if err != nil {
			return err
		}

		err = genContractBindings(abiFilePath, bytecodeFilePath, goPackageName, contract.Name)
		if err != nil {
			return err
		}

		contractMetaData := contractMetadata{
			Name:        contract.Name,
			DeployedBin: contract.DeployedBytecode,
			Package:     goPackageName,
		}

		if err := writeContractMetadata(contractMetaData, metadataOutputDir, contract.Name, contractMetadataFileTemplate); err != nil {
			return err
		}
	}

	for _, contract := range contracts.Immutables.ProxyDeployment {
		log.Printf("Generating bindings and metadata for verified Etherscan contract with immutables and proxy deployment: %s", contract.Name)

		contract.Abi, err = fetchAbi(fmt.Sprintf(etherscanGetAbiUrl, contract.DeployedAddress, etherscanApiKey), apiMaxRetries, apiRetryDelay)
		if err != nil {
			return err
		}

		deploymentTxHash, err := fetchDeploymentTxHash(fmt.Sprintf(etherscanGetDeploymentTxHashUrl, contract.DeployedAddress, etherscanApiKey), apiMaxRetries, apiRetryDelay)
		if err != nil {
			return err
		}
		contract.InitBytecode, err = fetchDeploymentData(fmt.Sprintf(etherscanGetTxByHashUrl, deploymentTxHash, etherscanApiKey), apiMaxRetries, apiRetryDelay)
		if err != nil {
			return err
		}

		abiFilePath, bytecodeFilePath, err := writeContractArtifacts(tempArtifactsDir, contract.Name, []byte(contract.Abi), []byte(contract.InitBytecode))
		if err != nil {
			return err
		}

		err = genContractBindings(abiFilePath, bytecodeFilePath, goPackageName, contract.Name)
		if err != nil {
			return err
		}

		contractMetaData := contractWithImmutablesMetadata{
			Name:           contract.Name,
			InitBin:        contract.InitBytecode,
			Package:        goPackageName,
		}

		contractMetadataWithImmutablesFileTemplate := template.Must(template.New("contractMetadataWithImmutables").Parse(contractMetadataWithImmutablesTemplate))
		if err := writeContractWithImmutablesMetadata(contractMetaData, metadataOutputDir, contract.Name, contractMetadataWithImmutablesFileTemplate); err != nil {
			return err
		}
	}

	return nil
}

// contractMetadataTemplate is a Go text template for generating the metadata
// associated with a Etherscan Ethereum contract. This template is used to produce
// Go code containing necessary a constant and initialization logic for the contract's
// deployed bytecode.
//
// The template expects to be provided with:
// - .Package: the name of the Go package.
// - .Name: the name of the contract.
// - .DeployedBin: the binary (hex-encoded) of the deployed contract.
var contractMetadataTemplate = `// Code generated - DO NOT EDIT.
// This file is a generated binding and any manual changes will be lost.

package {{.Package}}

var {{.Name}}DeployedBin = "{{.DeployedBin}}"
func init() {
	deployedBytecodes["{{.Name}}"] = {{.Name}}DeployedBin
}
`

var contractMetadataWithImmutablesTemplate = `// Code generated - DO NOT EDIT.
// This file is a generated binding and any manual changes will be lost.

package {{.Package}}

var {{.Name}}InitBin = "{{.InitBin}}"
func init() {
	initBytecodes["{{.Name}}"] = {{.Name}}InitBin
}
`
