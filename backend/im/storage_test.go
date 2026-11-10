package im

import (
 "os"
 "path/filepath"
 "testing"
)

func TestStorageMigrationPreservesIdentityAndFiles(t *testing.T) {
 root:=t.TempDir(); old:=filepath.Join(root,"old"); s,_:=New(old)
 host:=call(t,s,"POST","/guest","",`{}`,200)
 os.WriteFile(s.filePath("example"),[]byte("payload"),0600)
 data,files:=filepath.Join(root,"data"),filepath.Join(root,"files")
 if err:=s.Relocate(data,files,func()error{return nil});err!=nil{t.Fatal(err)}
 if b,e:=os.ReadFile(s.filePath("example"));e!=nil||string(b)!="payload"{t.Fatal(e,string(b))}
 if _,e:=os.Stat(filepath.Join(old,"state.json"));e!=nil{t.Fatal("source removed")}
 again,_:=NewWithFiles(data,files); restored:=call(t,again,"POST","/guest","",`{}`,200)
 if host["token"]!=restored["token"]{t.Fatal("identity lost")}
 if e:=s.Relocate(old,files,func()error{return nil});e==nil{t.Fatal("nonempty destination accepted")}
}
