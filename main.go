package main

import (
	"archive/zip"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const metaURL = "https://bmclapi2.bangbang93.com/neoforge/list/1.21.7"
const baseURL = "https://maven.neoforged.net/releases"

type NeoForgeVersion struct {
	Version       string `json:"version"`
	InstallerPath string `json:"installerPath"`
	McVersion     string `json:"mcversion"`
	RawVersion    string `json:"rawVersion"`
}

type MavenMetadata struct {
	Versioning struct {
		Latest string `xml:"latest"`
	} `xml:"versioning"`
}

// 从 installer jar 解压 version.json 到 outDir
func extractVersionJson(jarPath, outDir string) error {
	zipReader, err := zip.OpenReader(jarPath)
	if err != nil {
		return err
	}
	defer zipReader.Close()
	for _, f := range zipReader.File {
		if f.Name == "version.json" {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			outPath := filepath.Join(outDir, "version.json")
			outFile, err := os.Create(outPath)
			if err != nil {
				return err
			}
			defer outFile.Close()
			_, err = io.Copy(outFile, rc)
			if err != nil {
				return err
			}
			fmt.Printf("Extracted version.json to %s\n", outPath)
			return nil
		}
	}
	return fmt.Errorf("version.json not found in %s", jarPath)
}

// 自动下载installer，优先BMCLAPI，失败则用官方maven
func downloadInstaller(installerPath, fileName, buildDir string) error {
	// bmclapiURL := "https://bmclapi2.bangbang93.com" + installerPath
	officialURL := "https://maven.neoforged.net/releases" + installerPath
	urls := []string{officialURL}
	var lastErr error
	for _, url := range urls {
		fmt.Printf("尝试下载 %s\n", url)
		resp, err := http.Get(url)
		if err != nil {
			fmt.Printf("下载失败: %v\n", err)
			lastErr = err
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			fmt.Printf("下载失败: 状态码 %d\n", resp.StatusCode)
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
			continue
		}
		outFile, err := os.Create(filepath.Join(buildDir, fileName))
		if err != nil {
			return fmt.Errorf("Error creating file: %v", err)
		}
		defer outFile.Close()
		_, err = io.Copy(outFile, resp.Body)
		if err != nil {
			return fmt.Errorf("Error saving installer: %v", err)
		}
		fmt.Printf("成功下载 %s\n", url)
		return nil
	}
	return fmt.Errorf("所有源下载失败: %v", lastErr)
}

// 自动创建 launcher_profiles.json
func ensureLauncherProfile(destDir string) error {
	profilePath := filepath.Join(destDir, "launcher_profiles.json")
	if _, err := os.Stat(profilePath); err == nil {
		// 已存在
		return nil
	}
	profile := map[string]interface{}{
		"profiles": map[string]interface{}{},
		"selectedProfile": "",
		"clientToken": "",
		"authenticationDatabase": map[string]interface{}{},
		"settings": map[string]interface{}{},
	}
	f, err := os.Create(profilePath)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(profile)
}

// 修改BuildNeoForgeClient调用下载逻辑
func BuildNeoForgeClient(nf NeoForgeVersion) (string, error) {
	// 构建目录为 build/<mc_version>/<neoforge_version>
	buildDir := filepath.Join("build", nf.McVersion, nf.Version)
	fmt.Printf("\nBuilding NeoForge client for Minecraft %s with NeoForge %s...\n", nf.McVersion, nf.Version)

	// 创建 build/MC/版本 目录
	err := os.MkdirAll(buildDir, os.ModePerm)
	if err != nil {
		return "", fmt.Errorf("Error creating build directory: %v", err)
	}

	// 自动创建 launcher_profiles.json
	if err := ensureLauncherProfile(buildDir); err != nil {
		return "", fmt.Errorf("Error creating launcher_profiles.json: %v", err)
	}

	fileName := filepath.Base(nf.InstallerPath)
	installerPath := filepath.Join(buildDir, fileName)

	// 跳过已存在的 client.jar
	clientJarName := fmt.Sprintf("neoforge-%s-client.jar", nf.Version)
	clientJarPath := filepath.Join(buildDir, clientJarName)
	if _, err := os.Stat(clientJarPath); err == nil {
		fmt.Printf("Already built: %s, skip.\n", clientJarPath)
		return clientJarPath, nil
	}

	fmt.Printf("Downloading %s to %s\n", fileName, installerPath)
	if err := downloadInstaller(nf.InstallerPath, fileName, buildDir); err != nil {
		return "", fmt.Errorf("Error downloading installer: %v", err)
	}

	fmt.Printf("Downloaded installer to %s\n", installerPath)

	fmt.Println("Running NeoForge installer with --install-client...")
	cmd := exec.Command("java", "-jar", fileName, "--install-client", ".")
	cmd.Dir = buildDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		return "", fmt.Errorf("Error running installer: %v", err)
	}

	// 复制 client.jar
	fmt.Println("Copying client jar...")
	sourceFileName := fmt.Sprintf("neoforge-%s-client.jar", nf.Version)
	sourcePath := filepath.Join(buildDir, sourceFileName)
	destDir := buildDir
	if err := os.MkdirAll(destDir, os.ModePerm); err != nil {
		return "", fmt.Errorf("Error creating destination directory %s: %v", destDir, err)
	}
	destPath := filepath.Join(destDir, sourceFileName)

	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return "", fmt.Errorf("Error opening source file %s: %v", sourcePath, err)
	}
	defer sourceFile.Close()

	destFile, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("Error creating destination file %s: %v", destPath, err)
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return "", fmt.Errorf("Error copying file from %s to %s: %v", sourcePath, destPath, err)
	}

	fmt.Printf("Successfully copied client jar to %s\n", destPath)

	// 解压 installer jar 里的 version.json 到 client jar 同目录
	err = extractVersionJson(installerPath, destDir)
	if err != nil {
		fmt.Printf("Warning: %v\n", err)
	}

	// 不再清理 build 目录，保留产物

	return destPath, nil
}

func getLatestNeoForgeVersion() (string, error) {
	resp, err := http.Get("https://maven.neoforged.net/net/neoforged/neoforge/maven-metadata.xml")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var meta MavenMetadata
	if err := xml.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return "", err
	}
	return meta.Versioning.Latest, nil
}

func main() {
	latest := flag.Bool("latest", false, "只构建最新NeoForge版本")
	mc := flag.String("mc", "", "指定Minecraft版本, 例如 1.21.7")
	flag.Parse()

	if *latest {
		// 1. 获取maven-metadata.xml中的latest版本
		latestVersion, err := getLatestNeoForgeVersion()
		if err != nil {
			fmt.Printf("Error fetching latest NeoForge version: %v\n", err)
			os.Exit(1)
		}
		// 2. 拼接installerPath（去掉/maven前缀）
		installerPath := fmt.Sprintf("/net/neoforged/neoforge/%s/neoforge-%s-installer.jar", latestVersion, latestVersion)
		// 3. 构造NeoForgeVersion结构体
		version := NeoForgeVersion{
			Version:       latestVersion,
			InstallerPath: installerPath,
			McVersion:     "", // 可选，maven仓库不含mcversion
			RawVersion:    "neoforge-" + latestVersion,
		}
		fmt.Printf("\n==== 构建 NeoForge 最新版 %s ====\n", latestVersion)
		jarPath, err := BuildNeoForgeClient(version)
		if err != nil {
			fmt.Printf("构建失败: %v\n", err)
			os.Exit(1)
		}
		f, _ := os.Create("artifacts.txt")
		fmt.Fprintf(f, "%s %s\n", jarPath, latestVersion)
		f.Close()
		fmt.Printf("构建完成: %s\n", latestVersion)
		return
	}

	if *mc == "" {
		fmt.Println("请使用 --mc <version> 参数指定MC版本，或 --latest 构建最新版")
		os.Exit(1)
	}

	// 获取该 MC 版本下所有 NeoForge 版本（保留原有逻辑）
	metaURL := fmt.Sprintf("https://bmclapi2.bangbang93.com/neoforge/list/%s", *mc)
	resp, err := http.Get(metaURL)
	if err != nil {
		fmt.Printf("Error fetching NeoForge metadata: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Error fetching NeoForge metadata: status %d\n", resp.StatusCode)
		os.Exit(1)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading NeoForge metadata: %v\n", err)
		os.Exit(1)
	}

	var versions []NeoForgeVersion
	if err := json.Unmarshal(body, &versions); err != nil {
		fmt.Printf("Error parsing NeoForge metadata: %v\n", err)
		os.Exit(1)
	}
	if len(versions) == 0 {
		fmt.Println("未找到任何 NeoForge 版本")
		os.Exit(1)
	}

	sort.Slice(versions, func(i, j int) bool {
		return versions[i].Version < versions[j].Version
	})
	latestVersion := versions[len(versions)-1]

	// 修正installerPath，去掉/maven前缀
	latestVersion.InstallerPath = strings.Replace(latestVersion.InstallerPath, "/maven", "", 1)

	fmt.Printf("\n==== 构建 %s / %s ====\n", latestVersion.McVersion, latestVersion.Version)
	jarPath, err := BuildNeoForgeClient(latestVersion)
	if err != nil {
		fmt.Printf("构建失败: %v\n", err)
		os.Exit(1)
	}
	f, _ := os.Create("artifacts.txt")
	fmt.Fprintf(f, "%s %s %s\n", jarPath, latestVersion.McVersion, latestVersion.Version)
	f.Close()
	fmt.Printf("构建完成: %s %s\n", latestVersion.McVersion, latestVersion.Version)
} 
