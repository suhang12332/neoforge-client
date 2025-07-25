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
	// "sort"
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

// 通用：从 jar/zip 文件中提取指定文件到 outDir
func extractFileFromJar(jarPath, outDir, fileName string) error {
	zipReader, err := zip.OpenReader(jarPath)
	if err != nil {
		return err
	}
	defer zipReader.Close()
	for _, f := range zipReader.File {
		if f.Name == fileName {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			outPath := filepath.Join(outDir, fileName)
			outFile, err := os.Create(outPath)
			if err != nil {
				return err
			}
			defer outFile.Close()
			_, err = io.Copy(outFile, rc)
			if err != nil {
				return err
			}
			fmt.Printf("Extracted %s to %s\n", fileName, outPath)
			return nil
		}
	}
	return fmt.Errorf("%s not found in %s", fileName, jarPath)
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
	buildDir := filepath.Join("build", nf.Version)
	fmt.Printf("\nBuilding NeoForge client for Minecraft %s with NeoForge %s...\n", nf.McVersion, nf.Version)

	// 创建 build/版本 目录
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
	outputDir := filepath.Join("build", nf.Version)
	clientJarPath := filepath.Join(outputDir, clientJarName)
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
	sourcePath := filepath.Join(buildDir, "libraries", "net", "neoforged", "neoforge", nf.Version, sourceFileName)

	destDir := outputDir
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

	// 解压 installer jar 里的 version.json 和 install_profile.json 到 client jar 同目录
	if err := extractFileFromJar(installerPath, destDir, "version.json"); err != nil {
		fmt.Printf("Warning: %v\n", err)
	}
	if err := extractFileFromJar(installerPath, destDir, "install_profile.json"); err != nil {
		fmt.Printf("Warning: %v\n", err)
	}

	// 自动 patch version.json，追加所有 neoforge universal library
	func() {
		ipPath := filepath.Join(destDir, "install_profile.json")
		vPath := filepath.Join(destDir, "version.json")
		ipBytes, err := os.ReadFile(ipPath)
		if err != nil {
			fmt.Printf("patch version.json: 读取 install_profile.json 失败: %v\n", err)
			return
		}
		var ip map[string]interface{}
		if err := json.Unmarshal(ipBytes, &ip); err != nil {
			fmt.Printf("patch version.json: 解析 install_profile.json 失败: %v\n", err)
			return
		}
		var targetLibs []map[string]interface{}
		if libs, ok := ip["libraries"].([]interface{}); ok {
			for _, l := range libs {
				lib, _ := l.(map[string]interface{})
				name, _ := lib["name"].(string)
				if lib != nil && strings.HasPrefix(name, "net.neoforged:neoforge:") && strings.HasSuffix(name, ":universal") {
					targetLibs = append(targetLibs, lib)
				}
			}
		}
		if len(targetLibs) == 0 {
			fmt.Printf("patch version.json: 未找到目标 library\n")
			return
		}
		vBytes, err := os.ReadFile(vPath)
		if err != nil {
			fmt.Printf("patch version.json: 读取 version.json 失败: %v\n", err)
			return
		}
		var v map[string]interface{}
		if err := json.Unmarshal(vBytes, &v); err != nil {
			fmt.Printf("patch version.json: 解析 version.json 失败: %v\n", err)
			return
		}
		if vlibs, ok := v["libraries"].([]interface{}); ok {
			for _, lib := range targetLibs {
				vlibs = append(vlibs, lib)
			}
			v["libraries"] = vlibs
		} else {
			libs := make([]interface{}, 0, len(targetLibs))
			for _, lib := range targetLibs {
				libs = append(libs, lib)
			}
			v["libraries"] = libs
		}
		out, _ := json.MarshalIndent(v, "", "  ")
		if err := os.WriteFile(vPath, out, 0644); err != nil {
			fmt.Printf("patch version.json: 写回 version.json 失败: %v\n", err)
			return
		}
		fmt.Println("patch version.json: 已追加所有 universal library 并更新 version.json")
	}()

	// 清理 build 目录
	// os.RemoveAll(buildDir)

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

func getLatestMCRelease() (string, error) {
	resp, err := http.Get("https://launchermeta.mojang.com/mc/game/version_manifest.json")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var data struct {
		Latest struct {
			Release string `json:"release"`
		} `json:"latest"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	return data.Latest.Release, nil
}

func main() {
	latest := flag.Bool("latest", false, "只构建最新NeoForge版本")
	mc := flag.String("mc", "", "指定Minecraft版本, 例如 1.21.7")
	flag.Parse()

	if *latest {
		// 1. 获取 MC 最新 release 版本
		latestMC, err := getLatestMCRelease()
		if err != nil {
			fmt.Printf("Error fetching latest MC release: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Latest MC release: %s\n", latestMC)

		// 2. 获取该 MC 版本下所有 NeoForge 版本
		metaURL := fmt.Sprintf("https://bmclapi2.bangbang93.com/neoforge/list/%s", latestMC)
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
		latestVersion := versions[len(versions)-1]
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

	// sort.Slice(versions, func(i, j int) bool {
	// 	return versions[i].Version < versions[j].Version
	// })
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
