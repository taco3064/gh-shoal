package cli

import (
 "bytes"
 "context"
 "encoding/base64"
 "encoding/json"
 "errors"
 "fmt"
 "net/url"
 "os"
 "os/exec"
 "path/filepath"
 "regexp"
 "strings"
)

const rootID = 1379044983
const summaryPath = ".github/workflows/reviewer-summary.yml"
var managedPaths = []string{".github/ISSUE_TEMPLATE/review-request.yml", summaryPath}
var repoName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type runner func(context.Context, string, ...string) ([]byte, error)
func systemRun(ctx context.Context, program string, args ...string) ([]byte, error) {
 cmd := exec.CommandContext(ctx, program, args...)
 // Neither repository hooks nor user-level hooks may run during init.
 if program == "git" { cmd.Args = append([]string{cmd.Args[0], "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "diff.external=false", "-c", "commit.gpgsign=false"}, cmd.Args[1:]...) }
 out, err := cmd.CombinedOutput()
 if err != nil { return nil, fmt.Errorf("%s failed: %w: %s", program, err, strings.TrimSpace(string(out))) }
 return out, nil
}
type repository struct {
 ID int64 `json:"id"`
 FullName string `json:"full_name"`
 Fork bool `json:"fork"`
 DefaultBranch string `json:"default_branch"`
 Owner struct { ID int64 `json:"id"`; Type string `json:"type"` } `json:"owner"`
 Parent *struct { ID int64 `json:"id"` } `json:"parent"`
 Source *struct { ID int64 `json:"id"` } `json:"source"`
}
type initCommand struct { run runner; dir string }
func NewInit() Handler {
 c := initCommand{run:systemRun, dir:"."}
 return c.execute
}
func (c initCommand) call(ctx context.Context, program string, args ...string) (string,error) {
 out,err:=c.run(ctx,program,args...); return strings.TrimSpace(string(out)),err
}
func (c initCommand) api(ctx context.Context, endpoint string, result any) error {
 out,err:=c.run(ctx,"gh","api",endpoint); if err!=nil{return err}; return json.Unmarshal(out,result)
}
func (c initCommand) git(ctx context.Context, args ...string)(string,error){return c.call(ctx,"git",append([]string{"-C",c.dir},args...)...)}
func (c initCommand) execute(ctx context.Context,args []string) error {
 if len(args)!=0 {return errors.New("usage: gh shoal init")}
 branch,err:=c.git(ctx,"symbolic-ref","--quiet","--short","HEAD"); if err!=nil||branch!="main" {return errors.New("init requires the local main branch")}
 if err=c.clean(ctx);err!=nil{return err}
 pre,err:=c.git(ctx,"rev-parse","HEAD");if err!=nil{return err}
 if _,err=c.call(ctx,"gh","auth","status");err!=nil{return fmt.Errorf("gh auth is required: %w",err)}
 var viewer struct {ID int64 `json:"id"`}
 if err=c.api(ctx,"user",&viewer);err!=nil{return fmt.Errorf("cannot verify authenticated GitHub user: %w",err)}
 if viewer.ID==0{return errors.New("cannot verify authenticated GitHub user ID")}
 remotes,err:=c.git(ctx,"remote");if err!=nil{return err}
 var node repository; var remote string
 for _,name:=range strings.Fields(remotes){
  raw,e:=c.git(ctx,"remote","get-url",name);if e!=nil{return e}
  locator,e:=repositoryLocator(raw);if e!=nil{continue}
  var candidate repository
  if e=c.api(ctx,"repos/"+locator,&candidate);e!=nil{return e}
  if candidate.ID!=rootID&&candidate.Fork&&candidate.Parent!=nil&&candidate.Parent.ID==rootID&&candidate.Owner.Type=="User"&&candidate.Owner.ID==viewer.ID {
   if remote!=""&&node.ID!=candidate.ID{return errors.New("multiple eligible Reviewer Node remotes")}
   remote=name;node=candidate
  }
 }
 if remote==""{return errors.New("no direct Personal Account fork owned by the authenticated user is configured as a remote")}
 if !repoName.MatchString(node.FullName){return errors.New("invalid Reviewer Node locator")}
 if err=c.validateNode(node,viewer.ID);err!=nil{return err}
 remoteHead,err:=c.git(ctx,"ls-remote","--exit-code",remote,"refs/heads/main");if err!=nil{return fmt.Errorf("cannot verify Reviewer Node remote main: %w",err)}
 fields:=strings.Fields(remoteHead);if len(fields)!=2||fields[0]!=pre||fields[1]!="refs/heads/main" {return errors.New("local main is not synchronized with Reviewer Node remote main")}
 var root repository
 if err=c.api(ctx,"repositories/1379044983",&root);err!=nil{return err}
 if root.ID!=rootID||!repoName.MatchString(root.FullName)||root.DefaultBranch=="" {return errors.New("canonical Network Root identity or default branch is invalid")}
 var ref struct{Object struct{ SHA string `json:"sha"` } `json:"object"`}
 if err=c.api(ctx,"repos/"+root.FullName+"/git/ref/heads/"+url.PathEscape(root.DefaultBranch),&ref);err!=nil{return err}
 if !regexp.MustCompile(`^[0-9a-fA-F]{40}$`).MatchString(ref.Object.SHA){return errors.New("invalid Network Root HEAD SHA")}
 contents:=make(map[string][]byte)
 for _,path:=range managedPaths {
  var file struct{Content string `json:"content"`; Encoding string `json:"encoding"`; Type string `json:"type"`}
  if err=c.api(ctx,"repos/"+root.FullName+"/contents/"+path+"?ref="+ref.Object.SHA,&file);err!=nil{return err}
  if file.Type!="file"||file.Encoding!="base64"{return fmt.Errorf("canonical %s is not a regular file",path)}
  b,e:=base64.StdEncoding.DecodeString(strings.Map(func(r rune)rune{if r=='\n'||r=='\r'{return -1};return r},file.Content));if e!=nil{return e};contents[path]=b
 }
 // A path controlled by the fork must never redirect writes outside the repository.
 for _,path:=range managedPaths {if err=safePath(c.dir,path);err!=nil{return err}}
 changed:=false
 for _,path:=range managedPaths { p:=filepath.Join(c.dir,path);b,e:=os.ReadFile(p);if e!=nil&&!os.IsNotExist(e){return e};if !bytes.Equal(b,contents[path]){changed=true};if e==nil{info,e:=os.Stat(p);if e!=nil{return e};if info.Mode().Perm()&0111!=0{changed=true}} }
 if changed {
  if err=c.sync(ctx,pre,remote,contents);err!=nil{return err}
 }
 if err=c.validateNode(node,viewer.ID);err!=nil{return err}
 if _,err=c.call(ctx,"gh","auth","status");err!=nil{return err}
 for _,path:=range managedPaths{b,e:=os.ReadFile(filepath.Join(c.dir,path));if e!=nil||!bytes.Equal(b,contents[path]){return fmt.Errorf("managed file %s failed post-init verification: %w",path,e)}}
 return c.clean(ctx)
}
func (c initCommand) clean(ctx context.Context)error{
 s,e:=c.run(ctx,"git","-C",c.dir,"status","--porcelain=v1","-z","--untracked-files=all");if e!=nil{return e};if len(s)!=0{return errors.New("init requires a clean index and working tree; commit or discard changes first")};return nil
}
func(c initCommand) validateNode(node repository,userID int64)error{
 if node.ID==rootID||!node.Fork||node.Parent==nil||node.Parent.ID!=rootID||node.Owner.Type!="User"||node.Owner.ID!=userID{return errors.New("repository is not a direct Personal Account fork owned by the authenticated Reviewer")};return nil
}
func repositoryLocator(raw string)(string,error){
 raw=strings.TrimSpace(raw);var path string
 if strings.HasPrefix(raw,"git@github.com:"){path=strings.TrimPrefix(raw,"git@github.com:")}else{u,e:=url.Parse(raw);if e!=nil||u.Hostname()!="github.com"||u.User!=nil||u.Scheme!="https"{return "",errors.New("unsupported remote URL")};path=strings.TrimPrefix(u.Path,"/")}
 path=strings.TrimSuffix(path,".git");if !repoName.MatchString(path){return "",errors.New("invalid remote repository locator")};return path,nil
}
func safePath(dir,path string)error{
 parts:=strings.Split(path,"/");p:=dir
 for i,part:=range parts {p=filepath.Join(p,part);info,e:=os.Lstat(p);if os.IsNotExist(e){continue};if e!=nil{return e};if info.Mode()&os.ModeSymlink!=0||i<len(parts)-1&&!info.IsDir()||i==len(parts)-1&&!info.Mode().IsRegular(){return fmt.Errorf("unsafe managed path: %s",path)}}
 return nil
}
func(c initCommand) sync(ctx context.Context,pre,remote string,contents map[string][]byte)(err error){
 // Restore only files this invocation touched, including previously absent paths.
 originals:=make(map[string][]byte);present:=make(map[string]bool);modes:=make(map[string]os.FileMode)
 for _,path:=range managedPaths{p:=filepath.Join(c.dir,path);b,e:=os.ReadFile(p);if e==nil{info,statErr:=os.Stat(p);if statErr!=nil{return statErr};originals[path]=b;present[path]=true;modes[path]=info.Mode().Perm()}else if !os.IsNotExist(e){return e}}
 touched:=false; committed:=false
 defer func(){if err!=nil&&touched {
  if committed {_,_=c.git(ctx,"reset","--mixed",pre)}
  for _,path:=range managedPaths{p:=filepath.Join(c.dir,path);if present[path]{_ = os.WriteFile(p,originals[path],0644);_ = os.Chmod(p,modes[path])}else{_ = os.Remove(p)}}
  _,_=c.git(ctx,"reset","--mixed",pre)
 }}()
 for _,path:=range managedPaths{
  p:=filepath.Join(c.dir,path);if e:=os.MkdirAll(filepath.Dir(p),0755);e!=nil{return e}
  touched=true
  if e:=os.WriteFile(p,contents[path],0644);e!=nil{return e}
  if e:=os.Chmod(p,0644);e!=nil{return e}
  // Hash bytes directly to avoid executing a repository-defined clean filter.
  cmd:=exec.CommandContext(ctx,"git","-C",c.dir,"hash-object","-w","--stdin");cmd.Stdin=bytes.NewReader(contents[path]);hash,e:=cmd.Output();if e!=nil{return e}
  if _,e=c.git(ctx,"update-index","--add","--cacheinfo","100644",strings.TrimSpace(string(hash)),path);e!=nil{return e}
 }
 diff,e:=c.git(ctx,"diff","--cached","--name-only","-z");if e!=nil{return e}
 allowed:=map[string]bool{};for _,p:=range managedPaths{allowed[p]=true}
 for _,p:=range strings.Split(diff,"\x00"){if p!=""&&!allowed[p]{return fmt.Errorf("unexpected staged path: %s",p)}}
 status,e:=c.run(ctx,"git","-C",c.dir,"status","--porcelain=v1","-z","--untracked-files=all");if e!=nil{return e}
 for _,entry:=range strings.Split(string(status),"\x00") {if entry==""{continue};if len(entry)<4||!allowed[entry[3:]]{return fmt.Errorf("unexpected changed path: %s",entry)}}
 if diff==""{return nil}
 if _,e=c.git(ctx,"commit","--no-verify","-m","Synchronize Shoal station managed files");e!=nil{return e};committed=true
 if _,e=c.git(ctx,"push","--no-verify",remote,"HEAD:refs/heads/main");e!=nil{return fmt.Errorf("push failed: %w",e)}
 return nil
}
