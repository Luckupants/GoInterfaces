package core

import (
	"errors"
	"gitlab.com/slon/shad-go/gitfame/internal/information"
	"gitlab.com/slon/shad-go/gitfame/internal/input_reader"
	"gitlab.com/slon/shad-go/gitfame/internal/organize_data"
	"gitlab.com/slon/shad-go/gitfame/internal/print_data"
	"gitlab.com/slon/shad-go/gitfame/pkg/progress_bar"
	"maps"
	"os/exec"
	"strconv"
	"strings"
)

func skipHeader(data []string) []string {
	var firstWord string
	for firstWord != "filename" {
		firstWord = strings.Split(data[0], " ")[0]
		data = data[1:]
	}
	return data
}

func parseBlame(answer map[string]*information.FameInfo, data []string, seenCommits map[string]string, seenNames map[string]struct{}, info information.InputInfo) ([]string, error) {
	linesAmount, err := strconv.Atoi(strings.Split(data[0], " ")[3])
	if err != nil {
		return nil, errors.Join(err, errors.New("error parsing git blame"))
	}
	commit := strings.Split(data[0], " ")[0]
	name := ""
	if _, ok := seenCommits[commit]; !ok {
		if *info.FlagUseCommitter {
			_, name, _ = strings.Cut(data[5], " ")
		} else {
			_, name, _ = strings.Cut(data[1], " ")
		}
		seenCommits[commit] = name
		data = skipHeader(data)[1:]
	} else {
		name = seenCommits[commit]
		data = data[2:]
	}
	seenNames[name] = struct{}{}
	if _, ok := answer[name]; !ok {
		answer[name] = &information.FameInfo{Name: name, Commits: make(map[string]struct{})}
	}
	answer[name].Commits[commit] = struct{}{}
	answer[name].LinesAmount += linesAmount
	for i := 0; i < linesAmount-1; i++ {
		commit = strings.Split(data[0], " ")[0]
		answer[name].Commits[commit] = struct{}{}
		data = data[2:]
	}
	return data, nil
}

func getLogData(info information.InputInfo, file string) (string, string, error) {
	cmd := exec.Command("git", "log", *info.FlagCommit, "--max-count=1", "--pretty=format:%an%n%cn%n%H", "--", file)
	cmd.Dir = *info.FlagPath
	output, err := cmd.Output()
	if err != nil {
		return "", "", err
	}
	answer := strings.Split(string(output), "\n")
	if *info.FlagUseCommitter {
		return answer[1], answer[2], nil
	}
	return answer[0], answer[2], nil
}

type channelInfo struct {
	file   string
	info   information.InputInfo
	answer map[string]*information.FameInfo
}

func fame(files []string, info information.InputInfo, answer map[string]*information.FameInfo, pb *progress_bar.ProgressBar) error {
	errorChannel := make(chan error, *info.FlagGoroutines)
	tasksChannel := make(chan channelInfo, *info.FlagGoroutines)
	resultsChannel := make(chan map[string]*information.FameInfo, len(files))
	defer close(errorChannel)
	for i := 0; i < *info.FlagGoroutines; i++ {
		go func() {
			for {
				task, ok := <-tasksChannel
				if !ok {
					break
				}
				result, err := blame(task.info, task.file)
				if err != nil {
					errorChannel <- err
					break
				}
				resultsChannel <- result
			}
		}()
	}
	for _, file := range files {
		tasksChannel <- channelInfo{info: info, file: file, answer: answer}
	}
	close(tasksChannel)
	for i := 0; i < len(files); i++ {
		if len(errorChannel) != 0 {
			err := <-errorChannel
			return err
		}
		result := <-resultsChannel
		for _, res := range result {
			if _, ok := answer[res.Name]; !ok {
				answer[res.Name] = res
			} else {
				answer[res.Name].FilesAmount += res.FilesAmount
				answer[res.Name].LinesAmount += res.LinesAmount
				maps.Copy(answer[res.Name].Commits, res.Commits)
			}
		}
		pb.UpdateProgress(i * 100 / len(files))
	}
	if len(errorChannel) != 0 {
		err := <-errorChannel
		return err
	}
	for _, val := range answer {
		val.CommitsAmount = len(val.Commits)
	}
	return nil
}

func blame(info information.InputInfo, file string) (map[string]*information.FameInfo, error) {
	var (
		seenCommits = make(map[string]string)
		seenNames   = make(map[string]struct{})
	)
	cmd := exec.Command("git", "blame", *info.FlagCommit, file, "--porcelain")
	cmd.Dir = *info.FlagPath
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	data := strings.Split(string(output), "\n")
	if len(data) == 1 {
		name, commit, err2 := getLogData(info, file)
		if err2 != nil {
			return nil, err2
		}
		result := information.FameInfo{Name: name, Commits: make(map[string]struct{})}
		result.Commits[commit] = struct{}{}
		result.FilesAmount++
		return map[string]*information.FameInfo{name: &result}, nil
	}
	result := make(map[string]*information.FameInfo)
	for len(data) > 1 {
		data, err = parseBlame(result, data, seenCommits, seenNames, info)
		if err != nil {
			return nil, err
		}
	}
	for name := range seenNames {
		result[name].FilesAmount++
	}
	return result, nil
}

func Execute(info information.InputInfo, pb *progress_bar.ProgressBar) error {
	pb.SendMessage("Loading files...")
	files, err := input_reader.GetFiles(info)
	if err != nil {
		return err
	}
	pb.SendMessage("Filtering...")
	files = input_reader.FilterExtensions(files, *info.FlagExtensions)
	files, err = input_reader.FilterLanguages(files, info)
	if err != nil {
		return err
	}
	files, err = input_reader.ExcludePatterns(files, *info.FlagExclude, *info.FlagPath)
	if err != nil {
		return err
	}
	files, err = input_reader.Restrict(files, *info.FlagRestrict, *info.FlagPath)
	if err != nil {
		return err
	}
	answer := make(map[string]*information.FameInfo)
	pb.SendMessage("Looking for commits...")
	err = fame(files, info, answer, pb)
	if err != nil {
		return err
	}
	pb.SendMessage("Preparing output...")
	ans, err := organize_data.PrepareForOutput(answer, info)
	if err != nil {
		return err
	}
	err = print_data.PrintAnswer(ans, info)
	if err != nil {
		return err
	}
	return nil
}
