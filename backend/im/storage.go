package im

import("errors";"io/fs";"os";"path/filepath")

func(s *Service) Relocate(dataDir, filesDir string, before func() error) error {
 s.mu.Lock(); defer s.mu.Unlock()
 if filepath.Clean(dataDir)==filepath.Clean(s.dir)||filepath.Clean(filesDir)==filepath.Clean(s.fileDir){return errors.New("目标目录必须不同")}
 if before!=nil {if err:=before();err!=nil{return err}}
 for _,d:=range []string{dataDir,filesDir}{if e:=os.MkdirAll(d,0700);e!=nil{return e}; entries,e:=os.ReadDir(d);if e!=nil{return e};if len(entries)>0{return errors.New("目标目录必须为空")}}
 if b,e:=os.ReadFile(filepath.Join(s.dir,"state.json"));e==nil {if e=os.WriteFile(filepath.Join(dataDir,"state.json"),b,0600);e!=nil{return e}} else if !os.IsNotExist(e){return e}
 e:=fs.WalkDir(os.DirFS(s.fileDir),".",func(path string,d fs.DirEntry,err error)error{if err!=nil{return err};if d.IsDir(){return nil};src:=filepath.Join(s.fileDir,path);dst:=filepath.Join(filesDir,path);if err:=os.MkdirAll(filepath.Dir(dst),0700);err!=nil{return err};b,e:=os.ReadFile(src);if e!=nil{return e};return os.WriteFile(dst,b,0600)});if e!=nil{return e}
 s.dir=dataDir;s.fileDir=filesDir;return nil
}
