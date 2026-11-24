 개요: 스크레이핑 전용으로 올라와 있는 https://books.toscrape.com/ 에서 https://jsoup.org/ 를 활용하여 카테고리별 책들의 정보를 긁어와서 psql에 저장하고 이를 REST로 조회할 수 있는 웹프로그램을 만든다. 
 Endpoint 명세
 특정 카테고리에서 명시된 가격 범위내의 책을 json 형식으로 나열
 <조회사이트의 주소>/books
 파라미터: 
 category: 명시 필수이며, 조회할 도서 카테고리명을 문자열로 입력 받는다
 minPrice: 역시 명시 필수이며, 최소 가격을 실수로 명시
 maxPrice: 역시 명시 필수이며, 최대 가격을 실수로 명시
 
 조회 예시: GET /books?category=Travel&minPrice=10&maxPrice=30
 10이상 30이하의 가격대의 책을 "Travel" 분야에서 찾아서 준다. 
 
 오류 처리
 
 1. 존재하지 않는 카테고리에 대해서 물어보는 경우: 
 "존재하지 않는 카테고리입니다" 라는 오류 메시지를 JSON 형식으로 반환한다. 오류 메시지는 message에 는 것으로 한다. 
 
 {
 "message" : "존재하지 않는 카테고리입니다"
 }
 
 2. 가격 범위의 오류시 그에 합당한 오류 메시지를 생성해서 반환한다. 
 minPrice가 maxPrice보다 큰 경우에 대해서는 "minPrice가 maxPrice보다 높아서는 안됩니다"라고 한다
 
 {
 "message" : "minPrice가 maxPrice보다 높아서는 안됩니다"
 }
 
 필수 요건
 1. Spring Boot 프레임워크에 따라서 GET mapping을 하는 controller bean이 있어야 함
 2. PSQSL JDBC를 통해서 Neon의 PQSL을 접근할 수 있도록 구현되어 있어야 함
 3. 링크에 있는 온라인 서점은 반드시 jsoup을 통해서 크롤링하고 스크랩핑한다. 수동으로 PQSL 테이블에 놓지 않는다. 
 
 완료 후 프로젝트를 maven zip으로 export하여 제출하도록 한다. 반드시 빌드를 클린시킨 후에 소스만 제출하는 것으로 한다. zip에는 소스코드에 대한 간략한 설명이 담긴 readme 파일을 반드시 포함하도록 한다. README에는 사용한 IDE, JDK 버전,maven 버전, SPRING 버전을 반드시 명시하도록 한다. 
 
 12월 4일 23시 55분까지 제출. 늦은 제출은 받지 않음.
All products | Books to Scrape - Sandbox
 