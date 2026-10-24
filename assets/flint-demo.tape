# flint-demo.tape — README "How It Works" 데모 GIF 생성 스크립트.
# 재생성: vhs assets/flint-demo.tape (리포 루트에서 — ./bin/flint 사용).
# 흐름: version → hot(Inject) → write(Record) → read(Work/Verify).
# 주의: Type 라인은 비인용(줄 끝까지 원문) — 테이프 파서가 \" 미지원.
Output assets/flint-demo.gif

Set FontSize 15
Set Width 980
Set Height 480
Set Theme "Dracula"
Set TypingSpeed 35ms
Set Shell zsh

Type ./bin/flint version
Enter
Sleep 600ms

Type ./bin/flint hot
Enter
Sleep 800ms

Type@60ms ./bin/flint write knowledge
Enter
Type@30ms '{"topic":"flint demo","summary":"배포 자동화 결정 — 태그 push 후 goreleaser가 Release에 첨부한다","confidence":0.9}'
Enter
Ctrl+D
Sleep 900ms

Type ./bin/flint read "flint demo"
Enter
Sleep 900ms
