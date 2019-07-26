protoc -I=. -I=$GOPATH/src -I=$GOPATH/src/github.com/gogo/protobuf/protobuf \
--gofast_out=\
Mgoogle/protobuf/any.proto=github.com/gogo/protobuf/types,\
Mgoogle/protobuf/duration.proto=github.com/gogo/protobuf/types,\
Mgoogle/protobuf/struct.proto=github.com/gogo/protobuf/types,\
Mgoogle/protobuf/timestamp.proto=github.com/gogo/protobuf/types,\
Mgoogle/protobuf/wrappers.proto=github.com/gogo/protobuf/types:. \
common/messages/eventmessages/generalTypes.proto \
common/messages/eventmessages/factoidBlock.proto \
common/messages/eventmessages/adminBlock.proto \
common/messages/eventmessages/entryCredit.proto \
common/messages/eventmessages/factomEvents.proto