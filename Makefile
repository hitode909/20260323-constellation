DEST_DIR := $(HOME)/Pictures/星座

.PHONY: build run clean data all publish

all: data build run

build:
	go build -o constellation .

run: build
	./constellation

clean:
	rm -f constellation
	rm -rf output/

publish: run
	mkdir -p "$(DEST_DIR)"
	cp output/*.png "$(DEST_DIR)/"
	@echo "$(DEST_DIR) に88枚コピーしました"

data: data/hygdata_v41.csv data/stellarium_modern.json

data/hygdata_v41.csv:
	mkdir -p data
	curl -sL "https://raw.githubusercontent.com/astronexus/HYG-Database/main/hyg/CURRENT/hygdata_v41.csv" -o $@

data/stellarium_modern.json:
	mkdir -p data
	curl -sL "https://raw.githubusercontent.com/Stellarium/stellarium/master/skycultures/modern/index.json" -o $@
