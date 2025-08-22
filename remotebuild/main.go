// main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"

	"encoding/json"

	"github.com/bramvdbogaerde/go-scp"
	scpclient "github.com/bramvdbogaerde/go-scp/auth"
	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v2"
)

type Config struct {
	Host      string `json:"host" yaml:"host"`
	User      string `json:"user" yaml:"user"`
	Key       string `json:"key" yaml:"key"`
	TargetDir string `json:"target_dir" yaml:"target_dir"`
}

func loadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml") {
		if err := yaml.Unmarshal(b, cfg); err != nil {
			return nil, err
		}
	} else {
		if err := json.Unmarshal(b, cfg); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// 返回 ssh.Client 和 scp.Client（struct）
func sshConnect(host, user, keyPath string) (*ssh.Client, scp.Client, error) {
	clientConfig, err := scpclient.PrivateKey(user, keyPath, ssh.InsecureIgnoreHostKey())
	if err != nil {
		return nil, scp.Client{}, err
	}

	scpClient := scp.NewClient(fmt.Sprintf("%s:22", host), &clientConfig)
	if err := scpClient.Connect(); err != nil {
		return nil, scp.Client{}, err
	}

	keyFile, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, scp.Client{}, err
	}
	signer, err := ssh.ParsePrivateKey(keyFile)
	if err != nil {
		return nil, scp.Client{}, err
	}
	sshCfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	sshClient, err := ssh.Dial("tcp", host+":22", sshCfg)
	if err != nil {
		return nil, scp.Client{}, err
	}

	return sshClient, scpClient, nil
}

func runSSHCommandLive(client *ssh.Client, cmd string) error {

	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	stdout, _ := session.StdoutPipe()
	stdin, _ := session.StdinPipe()

	stderr, _ := session.StderrPipe()

	go io.Copy(os.Stdout, stdout)
	go io.Copy(os.Stderr, stderr)
	go io.Copy(stdin, os.Stdin)

	if err := session.Start(cmd); err != nil {
		return err
	}

	return session.Wait()
}

func uploadFile(scpClient scp.Client, localPath, remotePath string) error {
	ctx := context.Background()
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return scpClient.CopyFile(ctx, f, remotePath, "0655")
}

func downloadFile(scpClient scp.Client, remotePath, localPath string) error {
	ctx := context.Background()
	outFile, err := os.Create(localPath)
	if err != nil {
		fmt.Printf("Failed to create local file %s: %v\n", localPath, err)

		return err
	}
	defer outFile.Close()
	return scpClient.CopyFromRemote(ctx, outFile, remotePath)
}

func main() {
	var cfgPath string
	flag.StringVar(&cfgPath, "config", "config.json", "Path to config file (JSON or YAML)")
	host := flag.String("host", "", "Remote host")
	user := flag.String("user", "", "SSH username")
	key := flag.String("key", "", "Path to private key")
	targetDir := flag.String("target-dir", "", "Local target directory")
	src := flag.String("src", "", "Local CMake project path (required)")
	remoteDir := flag.String("remote-dir", "", "Remote build directory (required)")
	buildType := flag.String("build-type", "Release", "CMake build type")
	cmakeArgs := flag.String("cmake-args", "", "Extra CMake args")
	artifactsList := flag.String("artifacts", "", "Comma-separated list of final files to download (optional)")
	flag.Parse()

	if *src == "" || *remoteDir == "" {
		fmt.Println("src and remote-dir are required")
		os.Exit(1)
	}

	cfg, _ := loadConfig(cfgPath) // 忽略加载失败

	h := *host
	if h == "" && cfg != nil {
		h = cfg.Host
	}
	u := *user
	if u == "" && cfg != nil {
		u = cfg.User
	}
	k := *key
	if k == "" && cfg != nil {
		k = cfg.Key
	}
	t := *targetDir
	if t == "" && cfg != nil {
		t = cfg.TargetDir
	}

	if h == "" || u == "" || k == "" || t == "" {
		fmt.Println("host, user, key, target-dir must be specified via flags or config")
		os.Exit(1)
	}

	fmt.Printf("Using host=%s user=%s key=%s target-dir=%s\n", h, u, k, t)

	sshClient, scpClient, err := sshConnect(h, u, k)
	if err != nil {
		log.Fatal(err)
	}
	defer sshClient.Close()
	defer scpClient.Close()

	remoteBuildDir := path.Join(*remoteDir, "build")
	fmt.Printf("remoteBuildDir:", remoteBuildDir)
	fmt.Printf("mkdir -p %s", remoteBuildDir)
	runSSHCommandLive(sshClient, fmt.Sprintf("mkdir -p %s", remoteBuildDir))

	// 上传源代码

	filepath.Walk(*src, func(localPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(*src, localPath)
		// 转换为 Linux 风格路径，并使用 path.Join 拼接远程路径
		remotePath := path.Join(*remoteDir, filepath.ToSlash(rel))
		dir := path.Dir(remotePath)
		runSSHCommandLive(sshClient, fmt.Sprintf("mkdir -p %s", dir))
		uploadFile(scpClient, localPath, remotePath)
		return nil
	})

	fmt.Println("[3] Configuring CMake project remotely...")
	runSSHCommandLive(sshClient, fmt.Sprintf("cd %s && cmake .. -DCMAKE_BUILD_TYPE=%s %s", remoteBuildDir, *buildType, *cmakeArgs))

	fmt.Println("[4] Building project remotely...")
	runSSHCommandLive(sshClient, fmt.Sprintf("cd %s && cmake --build . -- -j$(nproc)", remoteBuildDir))

	// 下载 artifacts
	var artifacts []string
	if *artifactsList != "" {
		artifacts = strings.Split(*artifactsList, ",")
	}
	if len(artifacts) > 0 {
		for _, art := range artifacts {
			remotePath := path.Join(remoteBuildDir, art)
			localPath := filepath.Join(t, filepath.Base(art))

			downloadFile(scpClient, remotePath, localPath)
		}
	} else {
		// 下载整个构建目录

		fmt.Println("No artifacts specified, you can modify code to download whole build dir if needed")
	}

	fmt.Println("[Done] Remote build finished successfully")
}
