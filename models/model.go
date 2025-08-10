package models

type Video struct {
	Id         uint       `json:"id" gorm:"primaryKey"`
	Title      string     `json:"title"`
	Tags       []Tags     `json:"tags" gorm:"many2many:video_tags;"`
	Href       string     `json:"href"`
	Link       string     `json:"link"`
	Prediction Prediction `json:"prediction" gorm:"embedded"`
	Img        string     `json:"img"`
}

type Tags struct {
	ID   uint   `json:"id" gorm:"primaryKey"`
	Href string `json:"href"`
	Name string `json:"name" gorm:"unique"`
}

type Prediction struct {
	Drawings float32 `json:"drawings"`
	Hentai   float32 `json:"hentai"`
	Neutral  float32 `json:"neutral"`
	Porn     float32 `json:"porn"`
	Sexy     float32 `json:"sexy"`
}
