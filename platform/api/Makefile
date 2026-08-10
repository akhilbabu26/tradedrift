PROTO_ROOT := ../../proto
OUT_ROOT   := ./gen

.PHONY: compile

compile:
	@echo "Compiling auth protos..."
	protoc \
		--proto_path=$(PROTO_ROOT) \
		--go_out=$(OUT_ROOT) \
		--go_opt=paths=source_relative \
		--go-grpc_out=$(OUT_ROOT) \
		--go-grpc_opt=paths=source_relative \
		auth/v1/auth.proto

	@echo "Compiling wallet protos..."
	protoc \
		--proto_path=$(PROTO_ROOT) \
		--go_out=$(OUT_ROOT) \
		--go_opt=paths=source_relative \
		--go-grpc_out=$(OUT_ROOT) \
		--go-grpc_opt=paths=source_relative \
		wallet/v1/wallet.proto

	@echo "Compiling order protos..."
	protoc \
		--proto_path=$(PROTO_ROOT) \
		--go_out=$(OUT_ROOT) \
		--go_opt=paths=source_relative \
		--go-grpc_out=$(OUT_ROOT) \
		--go-grpc_opt=paths=source_relative \
		order/v1/order.proto

	@echo "Done."
