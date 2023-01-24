package utils

type Usage struct {
	Command     string
	Description string
}

type Code struct {
	Prompt    string
	Statement string
	Result    string
}

type Codes struct {
	Name string
	Code []Code
}

type Demo struct {
	Name   		string
	Prefix		string			// usefull in some cases to set prefix command
	Github 		string
	Codes  		[]Codes
	Usage  		[]Usage
	Doc    		string
	Content		string				// initial contents
	Compiler 	string	`json:"-"`	//ignore in json
}

type DemoResp struct {
	Status   string
	ErrorMsg string
	Demo     Demo
}

type DemoList struct {
	Demos []Demo `xml:"Demo"`
}

var Commands2DemoMap map[string]Demo

func GetCompilationScript(command string) string {
	demo, ok := Commands2DemoMap[command]
	if !ok {
		return ""
	}
	return demo.Compiler
}

func GetPrefix(command string) string {
        demo, ok := Commands2DemoMap[command]
        if !ok {
                return ""
        }
        return demo.Prefix
}
